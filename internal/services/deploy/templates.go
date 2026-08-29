package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/IamYGT/heyserver/internal/models"
)

const (
	deployTemplateSchemaVersion = 1
	maxDeployTemplateFiles      = 128
	maxDeployTemplateBytes      = 64 << 10
)

var deployTemplateIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type deployTemplateFile struct {
	SchemaVersion  int               `json:"schemaVersion"`
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Branch         string            `json:"branch"`
	DeploymentKind models.DeployKind `json:"deploymentKind"`
	ComposeFile    string            `json:"composeFile"`
	DeployScript   string            `json:"deployScript"`
}

// Templates observes reusable deployment presets from the installation-owned
// data directory. The API can select a returned template but cannot write a
// file, choose another directory, or introduce a command through this route.
func (s *Service) Templates() models.DeployTemplateInventory {
	inventory := models.DeployTemplateInventory{
		Status:    models.DeployTemplatesNotConfigured,
		Directory: s.templatesDir,
		Templates: []models.DeployTemplate{},
		Issues:    []models.DeployTemplateIssue{},
	}
	if s.templatesDir == "" {
		return inventory
	}
	directoryInfo, err := os.Stat(s.templatesDir)
	if os.IsNotExist(err) {
		return inventory
	}
	if err != nil {
		return unavailableTemplateInventory(inventory, "deploy-templates", "template directory cannot be inspected")
	}
	if !directoryInfo.IsDir() {
		return unavailableTemplateInventory(inventory, "deploy-templates", "template path is not a directory")
	}
	if directoryInfo.Mode().Perm()&0o022 != 0 {
		inventory.Issues = append(inventory.Issues, models.DeployTemplateIssue{
			File: "deploy-templates", Message: "template directory must not be group- or world-writable",
		})
	}
	entries, err := os.ReadDir(s.templatesDir)
	if err != nil {
		return unavailableTemplateInventory(inventory, "deploy-templates", "template directory cannot be read")
	}
	jsonFiles := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		jsonFiles++
		if jsonFiles > maxDeployTemplateFiles {
			inventory.Issues = append(inventory.Issues, models.DeployTemplateIssue{
				File: entry.Name(), Message: "template inventory exceeds 128 JSON files",
			})
			break
		}
		template, issue := s.readDeployTemplate(entry)
		if issue != nil {
			inventory.Issues = append(inventory.Issues, *issue)
			continue
		}
		inventory.Templates = append(inventory.Templates, *template)
	}
	sort.Slice(inventory.Templates, func(i, j int) bool {
		return inventory.Templates[i].ID < inventory.Templates[j].ID
	})
	if len(inventory.Issues) > 0 {
		inventory.Status = models.DeployTemplatesUnavailable
	} else if len(inventory.Templates) > 0 {
		inventory.Status = models.DeployTemplatesHealthy
	}
	return inventory
}

func unavailableTemplateInventory(inventory models.DeployTemplateInventory, file, message string) models.DeployTemplateInventory {
	inventory.Status = models.DeployTemplatesUnavailable
	inventory.Issues = append(inventory.Issues, models.DeployTemplateIssue{File: file, Message: message})
	return inventory
}

func (s *Service) readDeployTemplate(entry os.DirEntry) (*models.DeployTemplate, *models.DeployTemplateIssue) {
	issue := func(message string) (*models.DeployTemplate, *models.DeployTemplateIssue) {
		return nil, &models.DeployTemplateIssue{File: entry.Name(), Message: message}
	}
	id := strings.TrimSuffix(entry.Name(), ".json")
	if !deployTemplateIDPattern.MatchString(id) {
		return issue("filename must be a lowercase template ID followed by .json")
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return issue("symbolic links are not accepted")
	}
	info, err := entry.Info()
	if err != nil {
		return issue("template file cannot be inspected")
	}
	if !info.Mode().IsRegular() {
		return issue("template path is not a regular file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return issue("template file must not be group- or world-writable")
	}
	if info.Size() > maxDeployTemplateBytes {
		return issue("template file exceeds 64 KiB")
	}
	content, err := os.ReadFile(filepath.Join(s.templatesDir, entry.Name()))
	if err != nil {
		return issue("template file cannot be read")
	}
	if len(content) > maxDeployTemplateBytes {
		return issue("template file exceeds 64 KiB")
	}
	var file deployTemplateFile
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return issue("template JSON is invalid: " + boundedTemplateDecodeError(err))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return issue("template JSON must contain exactly one object")
	}
	template, err := normalizeDeployTemplate(id, file)
	if err != nil {
		return issue(err.Error())
	}
	return template, nil
}

func normalizeDeployTemplate(filenameID string, file deployTemplateFile) (*models.DeployTemplate, error) {
	file.ID = strings.TrimSpace(file.ID)
	file.Name = strings.TrimSpace(file.Name)
	file.Description = strings.TrimSpace(file.Description)
	file.Branch = strings.TrimSpace(file.Branch)
	file.ComposeFile = strings.TrimSpace(file.ComposeFile)
	if file.SchemaVersion != deployTemplateSchemaVersion {
		return nil, errors.New("schemaVersion must be 1")
	}
	if file.ID != filenameID || !deployTemplateIDPattern.MatchString(file.ID) {
		return nil, errors.New("id must match the template filename")
	}
	if file.Name == "" || len(file.Name) > 128 || strings.ContainsAny(file.Name, "\x00\r\n") {
		return nil, errors.New("name must be one line of 1 to 128 bytes")
	}
	if len(file.Description) > 512 || strings.ContainsRune(file.Description, '\x00') {
		return nil, errors.New("description must be at most 512 bytes")
	}
	if file.Branch == "" {
		file.Branch = "main"
	}
	if !validGitBranch(file.Branch) {
		return nil, errors.New("branch is invalid")
	}
	switch file.DeploymentKind {
	case models.DeployKindCompose:
		if strings.TrimSpace(file.DeployScript) != "" {
			return nil, errors.New("compose templates cannot define deployScript")
		}
		if file.ComposeFile != "" && !validComposeFile(file.ComposeFile) {
			return nil, errors.New("composeFile must remain relative to the project directory")
		}
	case models.DeployKindScript:
		if file.ComposeFile != "" {
			return nil, errors.New("script templates cannot define composeFile")
		}
		if strings.TrimSpace(file.DeployScript) == "" {
			return nil, errors.New("script templates require deployScript")
		}
		if err := validateDeployScript(file.DeployScript); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("deploymentKind must be compose or script")
	}
	return &models.DeployTemplate{
		ID: file.ID, Name: file.Name, Description: file.Description, Branch: file.Branch,
		DeployKind: file.DeploymentKind, ComposeFile: file.ComposeFile, DeployScript: file.DeployScript,
	}, nil
}

func boundedTemplateDecodeError(err error) string {
	message := err.Error()
	if len(message) > 160 {
		return fmt.Sprintf("%s…", message[:159])
	}
	return message
}
