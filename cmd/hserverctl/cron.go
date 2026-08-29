package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	cronsvc "github.com/IamYGT/heyserver/internal/services/cron"
)

var (
	localCronJobIDPattern  = regexp.MustCompile(`^[a-f0-9]{16}$`)
	remoteCronJobIDPattern = regexp.MustCompile(`^cron-[a-f0-9]{12}$`)
	cronRevisionPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	cronUserPattern        = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

type localCronJob struct {
	ID          string `json:"id"`
	User        string `json:"user"`
	Schedule    string `json:"schedule"`
	Command     string `json:"command"`
	Description string `json:"description"`
	IsActive    bool   `json:"isActive"`
}

type remoteCronJob struct {
	ID          string `json:"id,omitempty"`
	Schedule    string `json:"schedule"`
	User        string `json:"user"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type remoteCronSource struct {
	Path       string `json:"path"`
	EntryCount int    `json:"entry_count"`
	Managed    bool   `json:"managed"`
}

type remoteCronInventory struct {
	Service  string             `json:"service"`
	Jobs     []remoteCronJob    `json:"jobs"`
	Sources  []remoteCronSource `json:"sources"`
	Revision string             `json:"revision"`
}

type cronMutationOptions struct {
	Node        string
	Schedule    string
	User        string
	Command     string
	Description string
	Enabled     bool
	Wait        time.Duration
}

func runCron(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl cron status|list|system|create|update|delete|run")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: hserverctl cron status")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/cron/status", nil, true)
	case "list":
		node, err := parseOptionalNode("cron list", args[1:])
		if err != nil {
			return err
		}
		endpoint := "/api/cron/jobs"
		timeout := 30 * time.Second
		if node != "" {
			endpoint = "/api/nodes/" + url.PathEscape(node) + "/cron"
			timeout = 45 * time.Second
		}
		return printRequest(ctx, client.withTimeout(timeout), out, http.MethodGet, endpoint, nil, true)
	case "system":
		if len(args) != 1 {
			return errors.New("usage: hserverctl cron system")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/cron/system", nil, true)
	case "create":
		return runCronCreate(ctx, client, args[1:], out)
	case "update":
		return runCronUpdate(ctx, client, args[1:], out)
	case "delete":
		return runCronDelete(ctx, client, args[1:], out)
	case "run":
		return runCronRun(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown cron command %q", args[0])
	}
}

func runCronCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cron create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm creation of the scheduled command")
	schedule := flags.String("schedule", "", "five-field cron schedule")
	user := flags.String("user", "root", "Unix user")
	command := flags.String("command", "", "scheduled command")
	description := flags.String("description", "", "bounded job description")
	disabled := flags.Bool("disabled", false, "create the managed job disabled")
	wait := flags.Duration("wait", 2*time.Minute, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || strings.TrimSpace(*schedule) == "" || strings.TrimSpace(*command) == "" {
		return errors.New("usage: hserverctl cron create --confirm [--node NODE] --schedule SCHEDULE [--user USER] --command COMMAND [--description TEXT] [--disabled] [--wait DURATION]")
	}
	if !*confirmed {
		return errors.New("cron creation requires explicit --confirm")
	}
	options := cronMutationOptions{Node: strings.TrimSpace(*node), Schedule: strings.TrimSpace(*schedule), User: strings.TrimSpace(*user), Command: strings.TrimSpace(*command), Description: strings.TrimSpace(*description), Enabled: !*disabled, Wait: *wait}
	if err := validateCronMutation(options); err != nil {
		return err
	}
	if options.Node == "" {
		if !options.Enabled {
			return errors.New("--disabled is available only for managed-node cron creation; create then update a local job to disable it")
		}
		payload := map[string]any{"user": options.User, "schedule": options.Schedule, "command": options.Command, "description": options.Description}
		return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPost, "/api/cron/jobs", payload, true)
	}
	inventory, err := loadRemoteCronInventory(ctx, client, options.Node)
	if err != nil {
		return err
	}
	payload := remoteCronJob{Schedule: options.Schedule, User: options.User, Command: options.Command, Description: options.Description, Enabled: options.Enabled}
	return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPost, "/api/nodes/"+url.PathEscape(options.Node)+"/cron", cronRemotePayload(payload, inventory.Revision), true)
}

func runCronUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cron update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm replacement of the scheduled command")
	schedule := flags.String("schedule", "", "complete five-field cron schedule")
	user := flags.String("user", "", "complete Unix user")
	command := flags.String("command", "", "complete scheduled command")
	description := flags.String("description", "", "complete bounded job description")
	disabled := flags.Bool("disabled", false, "store the updated job disabled")
	wait := flags.Duration("wait", 2*time.Minute, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	positional := flags.Args()
	if len(positional) != 1 || strings.TrimSpace(*schedule) == "" || strings.TrimSpace(*user) == "" || strings.TrimSpace(*command) == "" {
		return errors.New("usage: hserverctl cron update --confirm [--node NODE] --schedule SCHEDULE --user USER --command COMMAND [--description TEXT] [--disabled] [--wait DURATION] JOB")
	}
	if !*confirmed {
		return errors.New("cron update requires explicit --confirm")
	}
	options := cronMutationOptions{Node: strings.TrimSpace(*node), Schedule: strings.TrimSpace(*schedule), User: strings.TrimSpace(*user), Command: strings.TrimSpace(*command), Description: strings.TrimSpace(*description), Enabled: !*disabled, Wait: *wait}
	if err := validateCronMutation(options); err != nil {
		return err
	}
	id := strings.TrimSpace(positional[0])
	if options.Node == "" {
		if !localCronJobIDPattern.MatchString(id) {
			return errors.New("local cron update requires an observed 16-character job identity")
		}
		current, err := loadLocalCronJob(ctx, client, id)
		if err != nil {
			return err
		}
		if current.User != options.User {
			return fmt.Errorf("local cron job %s belongs to user %s; changing job ownership is not supported", id, current.User)
		}
		endpoint := "/api/cron/jobs/" + url.PathEscape(id) + "?user=" + url.QueryEscape(current.User)
		payload := map[string]any{"schedule": options.Schedule, "command": options.Command, "description": options.Description, "isActive": options.Enabled}
		return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPut, endpoint, payload, true)
	}
	if !remoteCronJobIDPattern.MatchString(id) {
		return errors.New("managed cron update requires an observed cron- identity")
	}
	inventory, err := loadRemoteCronInventory(ctx, client, options.Node)
	if err != nil {
		return err
	}
	if _, err := findRemoteCronJob(inventory, id); err != nil {
		return err
	}
	payload := remoteCronJob{ID: id, Schedule: options.Schedule, User: options.User, Command: options.Command, Description: options.Description, Enabled: options.Enabled}
	endpoint := "/api/nodes/" + url.PathEscape(options.Node) + "/cron/" + url.PathEscape(id)
	return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPut, endpoint, cronRemotePayload(payload, inventory.Revision), true)
}

func runCronDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cron delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm deletion of the observed cron job")
	wait := flags.Duration("wait", 2*time.Minute, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl cron delete --confirm [--node NODE] [--wait DURATION] JOB")
	}
	if !*confirmed {
		return errors.New("cron deletion requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("cron action wait must be greater than zero")
	}
	id := strings.TrimSpace(flags.Args()[0])
	selectedNode := strings.TrimSpace(*node)
	if selectedNode == "" {
		if !localCronJobIDPattern.MatchString(id) {
			return errors.New("local cron deletion requires an observed 16-character job identity")
		}
		job, err := loadLocalCronJob(ctx, client, id)
		if err != nil {
			return err
		}
		endpoint := "/api/cron/jobs/" + url.PathEscape(id) + "?user=" + url.QueryEscape(job.User)
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, endpoint, nil, true)
	}
	if !remoteCronJobIDPattern.MatchString(id) {
		return errors.New("managed cron deletion requires an observed cron- identity")
	}
	inventory, err := loadRemoteCronInventory(ctx, client, selectedNode)
	if err != nil {
		return err
	}
	if _, err := findRemoteCronJob(inventory, id); err != nil {
		return err
	}
	endpoint := "/api/nodes/" + url.PathEscape(selectedNode) + "/cron/" + url.PathEscape(id)
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, endpoint, map[string]string{"revision": inventory.Revision}, true)
}

func runCronRun(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cron run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID")
	confirmed := flags.Bool("confirm", false, "confirm immediate execution of the observed job")
	wait := flags.Duration("wait", 3*time.Minute, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*node) == "" {
		return errors.New("usage: hserverctl cron run --confirm --node NODE [--wait DURATION] JOB")
	}
	if !*confirmed {
		return errors.New("cron manual run requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("cron action wait must be greater than zero")
	}
	id := strings.TrimSpace(flags.Args()[0])
	if !remoteCronJobIDPattern.MatchString(id) {
		return errors.New("managed cron run requires an observed cron- identity")
	}
	selectedNode := strings.TrimSpace(*node)
	inventory, err := loadRemoteCronInventory(ctx, client, selectedNode)
	if err != nil {
		return err
	}
	if _, err := findRemoteCronJob(inventory, id); err != nil {
		return err
	}
	endpoint := "/api/nodes/" + url.PathEscape(selectedNode) + "/cron/" + url.PathEscape(id) + "/run"
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
}

func validateCronMutation(options cronMutationOptions) error {
	if options.Wait <= 0 {
		return errors.New("cron action wait must be greater than zero")
	}
	if !cronUserPattern.MatchString(options.User) {
		return errors.New("cron user must be a portable lowercase Unix username")
	}
	if len(options.Command) == 0 || len(options.Command) > 4096 || strings.ContainsAny(options.Command, "\r\n\x00") {
		return errors.New("cron command must be 1-4096 control-free characters")
	}
	if len(options.Description) > 160 || strings.ContainsAny(options.Description, "\r\n\x00") {
		return errors.New("cron description must be at most 160 control-free characters")
	}
	if len(options.Schedule) > 160 || strings.ContainsAny(options.Schedule, "\r\n\x00") {
		return errors.New("cron schedule must be at most 160 control-free characters")
	}
	if err := cronsvc.ValidateExpression(options.Schedule); err != nil {
		return err
	}
	if options.Node != "" && len(strings.Fields(options.Schedule)) != 5 {
		return errors.New("managed cron schedules must use exactly five fields")
	}
	return nil
}

func loadLocalCronJob(ctx context.Context, client *apiClient, id string) (localCronJob, error) {
	response, err := requestJSON[struct {
		Jobs []localCronJob `json:"jobs"`
	}](ctx, client, http.MethodGet, "/api/cron/jobs", nil, true)
	if err != nil {
		return localCronJob{}, err
	}
	var matches []localCronJob
	for _, job := range response.Jobs {
		if job.ID == id {
			matches = append(matches, job)
		}
	}
	if len(matches) == 0 {
		return localCronJob{}, fmt.Errorf("local cron job %s is no longer present", id)
	}
	if len(matches) > 1 {
		return localCronJob{}, fmt.Errorf("local cron job %s is ambiguous across users", id)
	}
	return matches[0], nil
}

func loadRemoteCronInventory(ctx context.Context, client *apiClient, node string) (remoteCronInventory, error) {
	inventory, err := requestJSON[remoteCronInventory](ctx, client.withTimeout(45*time.Second), http.MethodGet,
		"/api/nodes/"+url.PathEscape(strings.TrimSpace(node))+"/cron", nil, true)
	if err != nil {
		return remoteCronInventory{}, err
	}
	if !cronRevisionPattern.MatchString(inventory.Revision) {
		return remoteCronInventory{}, errors.New("managed cron inventory returned an invalid revision")
	}
	for _, job := range inventory.Jobs {
		if !remoteCronJobIDPattern.MatchString(job.ID) {
			return remoteCronInventory{}, errors.New("managed cron inventory returned an invalid job identity")
		}
	}
	return inventory, nil
}

func findRemoteCronJob(inventory remoteCronInventory, id string) (remoteCronJob, error) {
	for _, job := range inventory.Jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return remoteCronJob{}, fmt.Errorf("managed cron job %s is no longer present", id)
}

func cronRemotePayload(job remoteCronJob, revision string) map[string]any {
	payload := map[string]any{
		"schedule": job.Schedule, "user": job.User, "command": job.Command,
		"description": job.Description, "enabled": job.Enabled, "revision": revision,
	}
	if job.ID != "" {
		payload["id"] = job.ID
	}
	return payload
}
