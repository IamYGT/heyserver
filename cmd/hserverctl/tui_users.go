package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/IamYGT/heyserver/internal/models"
)

type tuiUserFormKind string

const (
	tuiUserFormCreate   tuiUserFormKind = "create"
	tuiUserFormProfile  tuiUserFormKind = "profile"
	tuiUserFormPassword tuiUserFormKind = "password"
)

type tuiUserFormField struct {
	Key         string
	Label       string
	Value       string
	Placeholder string
	Maximum     int
	Secret      bool
}

type tuiUserForm struct {
	Kind          tuiUserFormKind
	Cursor        int
	Fields        []tuiUserFormField
	Original      models.User
	CurrentUserID int64
	Error         string
}

func (form tuiUserForm) rawValue(key string) string {
	for _, field := range form.Fields {
		if field.Key == key {
			return field.Value
		}
	}
	return ""
}

type tuiUsersState struct {
	Local         bool
	Supported     bool
	Message       string
	Users         []models.User
	CurrentUserID int64
	Total         int
}

type tuiUsersMsg struct {
	TargetID string
	State    tuiUsersState
	Err      error
}

type tuiUserListResponse struct {
	Data   []models.User `json:"data"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

func loadTUIUsersCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIUsers(ctx, client, target)
		return tuiUsersMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIUsers(ctx context.Context, client *apiClient, target tuiTarget) (tuiUsersState, error) {
	state := tuiUsersState{Local: target.Local}
	if !target.Local {
		state.Message = "Panel users belong to the central control plane; select Local."
		return state, nil
	}
	current, err := requestJSON[models.User](ctx, client, http.MethodGet, "/api/auth/me", nil, true)
	if err != nil {
		return state, fmt.Errorf("load current panel account: %w", err)
	}
	if current.ID <= 0 {
		return state, errors.New("current panel account returned an invalid identity")
	}
	inventory, err := loadTUIUserInventory(ctx, client)
	if err != nil {
		return state, err
	}
	state.Supported = true
	state.Users = inventory.Data
	state.CurrentUserID = current.ID
	state.Total = inventory.Total
	if inventory.Total > len(inventory.Data) {
		state.Message = fmt.Sprintf("Showing the newest %d of %d panel users; use hserverctl users list for older pages.", len(inventory.Data), inventory.Total)
	}
	return state, nil
}

func loadTUIUserInventory(ctx context.Context, client *apiClient) (tuiUserListResponse, error) {
	inventory, err := requestJSON[tuiUserListResponse](ctx, client, http.MethodGet, "/api/users?limit=200&offset=0", nil, true)
	if err != nil {
		return tuiUserListResponse{}, fmt.Errorf("load panel users: %w", err)
	}
	if inventory.Limit != 200 || inventory.Offset != 0 || inventory.Total < 0 || inventory.Total < len(inventory.Data) {
		return tuiUserListResponse{}, errors.New("panel-user inventory returned invalid pagination metadata")
	}
	seen := make(map[int64]bool, len(inventory.Data))
	for _, user := range inventory.Data {
		if user.ID <= 0 || seen[user.ID] {
			return tuiUserListResponse{}, errors.New("panel-user inventory returned an invalid or duplicate identity")
		}
		seen[user.ID] = true
		if _, err := validatePanelUserEmail(user.Email); err != nil {
			return tuiUserListResponse{}, fmt.Errorf("panel-user inventory: %w", err)
		}
		if _, err := validatePanelUserText("name", user.Name, 100, true); err != nil {
			return tuiUserListResponse{}, fmt.Errorf("panel-user inventory: %w", err)
		}
		if _, err := validatePanelUserRole(string(user.Role)); err != nil {
			return tuiUserListResponse{}, fmt.Errorf("panel-user inventory: %w", err)
		}
	}
	return inventory, nil
}

func (model tuiModel) loadUsers() (tea.Model, tea.Cmd) {
	model.resourceLoading = true
	model.notice = "Loading central panel users…"
	model.noticeError = false
	return model, loadTUIUsersCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) activateUserItem() (tea.Model, tea.Cmd) {
	if !model.usersLoaded {
		return model.loadUsers()
	}
	if !model.users.Local || !model.users.Supported {
		model.notice = valueOrNA(model.users.Message)
		model.noticeError = true
		return model, nil
	}
	if model.cursor < 0 || model.cursor >= len(model.users.Users) {
		return model, nil
	}
	model.openUserActions(model.users.Users[model.cursor])
	return model, nil
}

func (model *tuiModel) openUserActions(user models.User) {
	options := make([]tuiDialogOption, 0, 6)
	for _, role := range []models.Role{models.RoleAdmin, models.RoleManager, models.RoleViewer} {
		if user.Role == role {
			continue
		}
		options = append(options, tuiDialogOption{
			Label:     "Set role to " + string(role),
			Action:    "role-" + string(role),
			Dangerous: user.Role == models.RoleAdmin && role != models.RoleAdmin,
		})
	}
	options = append(options,
		tuiDialogOption{Label: "Edit name or email", Action: "edit-profile"},
		tuiDialogOption{Label: "Replace password", Action: "replace-password", Dangerous: true},
	)
	if user.ID != model.users.CurrentUserID {
		options = append(options, tuiDialogOption{Label: "Delete panel user", Action: "delete", Dangerous: true})
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage panel user · " + truncateTUI(user.Name, 38),
		Body: []string{
			truncateTUI(user.Email+" · "+string(user.Role), 68),
			"Every mutation re-observes the exact user and requires a separate confirmation.",
		},
		Options: options,
		Operation: tuiOperation{
			Kind: tuiOperationUser, Target: model.snapshot.Selected, User: user,
			CurrentUserID: model.users.CurrentUserID, Label: user.Name,
		},
	}
}

func (model *tuiModel) openUserCreateForm() {
	if !model.usersLoaded || !model.users.Local || !model.users.Supported || !model.snapshot.Selected.Local {
		model.notice, model.noticeError = "Panel-user creation requires the loaded central Users section", true
		return
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogUserForm, Title: "Create central panel user",
		Body: []string{"Password values stay masked and a separate confirmation follows."},
		UserForm: tuiUserForm{Kind: tuiUserFormCreate, CurrentUserID: model.users.CurrentUserID, Fields: []tuiUserFormField{
			{Key: "email", Label: "Email", Placeholder: "operator@example.com", Maximum: 254},
			{Key: "name", Label: "Name", Placeholder: "Operations", Maximum: 100},
			{Key: "role", Label: "Role", Value: string(models.RoleViewer), Placeholder: "admin, manager, or viewer", Maximum: 7},
			{Key: "password", Label: "Password", Placeholder: "minimum 8 characters", Maximum: maxPanelUserPasswordBytes, Secret: true},
			{Key: "confirm", Label: "Confirm", Placeholder: "repeat password", Maximum: maxPanelUserPasswordBytes, Secret: true},
		}},
	}
}

func (model *tuiModel) openUserProfileForm(user models.User) {
	model.dialog = tuiDialog{
		Mode: tuiDialogUserForm, Title: "Edit panel-user profile · " + truncateTUI(user.Name, 32),
		Body: []string{"Only changed fields are sent after the exact account is re-observed."},
		UserForm: tuiUserForm{Kind: tuiUserFormProfile, Original: user, CurrentUserID: model.users.CurrentUserID, Fields: []tuiUserFormField{
			{Key: "email", Label: "Email", Value: user.Email, Maximum: 254},
			{Key: "name", Label: "Name", Value: user.Name, Maximum: 100},
		}},
	}
}

func (model *tuiModel) openUserPasswordForm(user models.User) {
	model.dialog = tuiDialog{
		Mode: tuiDialogUserForm, Title: "Replace panel-user password · " + truncateTUI(user.Name, 26),
		Body: []string{"The password stays masked and the exact account is re-observed before replacement."},
		UserForm: tuiUserForm{Kind: tuiUserFormPassword, Original: user, CurrentUserID: model.users.CurrentUserID, Fields: []tuiUserFormField{
			{Key: "password", Label: "Password", Placeholder: "minimum 8 characters", Maximum: maxPanelUserPasswordBytes, Secret: true},
			{Key: "confirm", Label: "Confirm", Placeholder: "repeat password", Maximum: maxPanelUserPasswordBytes, Secret: true},
		}},
	}
}

func (model tuiModel) updateUserFormKey(key string) (tea.Model, tea.Cmd) {
	form := &model.dialog.UserForm
	if len(form.Fields) == 0 {
		model.dialog = tuiDialog{}
		return model, nil
	}
	switch key {
	case "esc":
		model.dialog = tuiDialog{}
		return model, nil
	case "tab", "down":
		form.Cursor = wrapIndex(form.Cursor+1, len(form.Fields))
		form.Error = ""
		return model, nil
	case "shift+tab", "up":
		form.Cursor = wrapIndex(form.Cursor-1, len(form.Fields))
		form.Error = ""
		return model, nil
	case "left", "right":
		field := &form.Fields[form.Cursor]
		if field.Key == "role" {
			roles := []string{string(models.RoleAdmin), string(models.RoleManager), string(models.RoleViewer)}
			index := 0
			for candidate, role := range roles {
				if field.Value == role {
					index = candidate
					break
				}
			}
			delta := 1
			if key == "left" {
				delta = -1
			}
			field.Value = roles[wrapIndex(index+delta, len(roles))]
			form.Error = ""
		}
		return model, nil
	case "backspace", "ctrl+h":
		runes := []rune(form.Fields[form.Cursor].Value)
		if len(runes) > 0 {
			form.Fields[form.Cursor].Value = string(runes[:len(runes)-1])
		}
		form.Error = ""
		return model, nil
	case "ctrl+u":
		form.Fields[form.Cursor].Value = ""
		form.Error = ""
		return model, nil
	case "enter":
		operation, err := operationFromTUIUserForm(*form, model.snapshot.Selected)
		if err != nil {
			form.Error = err.Error()
			return model, nil
		}
		model.openConfirmation(operation, confirmationBody(operation))
		return model, nil
	}
	if key == "space" {
		key = " "
	}
	if utf8.RuneCountInString(key) == 1 {
		character, _ := utf8.DecodeRuneInString(key)
		field := &form.Fields[form.Cursor]
		if !unicode.IsControl(character) && utf8.RuneCountInString(field.Value) < field.Maximum {
			field.Value += key
			form.Error = ""
		}
	}
	return model, nil
}

func operationFromTUIUserForm(form tuiUserForm, target tuiTarget) (tuiOperation, error) {
	if !target.Local {
		return tuiOperation{}, errors.New("panel-user control requires the central panel host")
	}
	switch form.Kind {
	case tuiUserFormCreate:
		email, err := validatePanelUserEmail(form.rawValue("email"))
		if err != nil {
			return tuiOperation{}, err
		}
		name, err := validatePanelUserText("name", form.rawValue("name"), 100, true)
		if err != nil {
			return tuiOperation{}, err
		}
		role, err := validatePanelUserRole(form.rawValue("role"))
		if err != nil {
			return tuiOperation{}, err
		}
		password, err := validateTUIUserPasswordConfirmation(form)
		if err != nil {
			return tuiOperation{}, err
		}
		return tuiOperation{
			Kind: tuiOperationUser, Target: target, Action: "create",
			DesiredUser:  models.User{Email: email, Name: name, Role: models.Role(role)},
			UserPassword: password, CurrentUserID: form.CurrentUserID,
			Label: "Create panel user " + email,
		}, nil
	case tuiUserFormProfile:
		if form.Original.ID <= 0 {
			return tuiOperation{}, errors.New("panel-user profile form has an invalid identity")
		}
		email, err := validatePanelUserEmail(form.rawValue("email"))
		if err != nil {
			return tuiOperation{}, err
		}
		name, err := validatePanelUserText("name", form.rawValue("name"), 100, true)
		if err != nil {
			return tuiOperation{}, err
		}
		if email == form.Original.Email && name == form.Original.Name {
			return tuiOperation{}, errors.New("change the panel-user email or name before continuing")
		}
		desired := form.Original
		desired.Email, desired.Name = email, name
		return tuiOperation{
			Kind: tuiOperationUser, Target: target, Action: "profile", User: form.Original,
			DesiredUser: desired, CurrentUserID: form.CurrentUserID,
			Label: "Update panel user " + form.Original.Email,
		}, nil
	case tuiUserFormPassword:
		if form.Original.ID <= 0 {
			return tuiOperation{}, errors.New("panel-user password form has an invalid identity")
		}
		password, err := validateTUIUserPasswordConfirmation(form)
		if err != nil {
			return tuiOperation{}, err
		}
		return tuiOperation{
			Kind: tuiOperationUser, Target: target, Action: "password", User: form.Original,
			UserPassword: password, CurrentUserID: form.CurrentUserID,
			Label: "Replace password for " + form.Original.Email, Dangerous: true,
		}, nil
	default:
		return tuiOperation{}, fmt.Errorf("unsupported panel-user form %q", form.Kind)
	}
}

func validateTUIUserPasswordConfirmation(form tuiUserForm) (string, error) {
	password, err := validatePanelUserPassword(form.rawValue("password"))
	if err != nil {
		return "", err
	}
	if password != form.rawValue("confirm") {
		return "", errors.New("panel-user password confirmation does not match")
	}
	return password, nil
}

func runTUIUserOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if !operation.Target.Local {
		return "", errors.New("panel-user control requires the central panel host")
	}
	if operation.Action == "create" {
		email, err := validatePanelUserEmail(operation.DesiredUser.Email)
		if err != nil {
			return "", err
		}
		name, err := validatePanelUserText("name", operation.DesiredUser.Name, 100, true)
		if err != nil {
			return "", err
		}
		role, err := validatePanelUserRole(string(operation.DesiredUser.Role))
		if err != nil {
			return "", err
		}
		password, err := validatePanelUserPassword(operation.UserPassword)
		if err != nil {
			return "", err
		}
		created, err := requestJSON[models.User](ctx, client, http.MethodPost, "/api/users", map[string]string{
			"email": email, "name": name, "role": role, "password": password,
		}, true)
		if err != nil {
			return "", err
		}
		if created.ID <= 0 || created.Email != email || created.Name != name || string(created.Role) != role {
			return "", errors.New("panel-user creation returned a mismatched receipt")
		}
		return "Created panel user " + created.Email, nil
	}
	if operation.User.ID <= 0 {
		return "", errors.New("panel-user operation has an invalid identity")
	}
	if operation.Action == "delete" && operation.CurrentUserID == operation.User.ID {
		return "", errors.New("cannot delete the current panel account")
	}
	inventory, err := loadTUIUserInventory(ctx, client)
	if err != nil {
		return "", err
	}
	current, found := findTUIUser(inventory.Data, operation.User.ID)
	if !found {
		return "", errors.New("panel user is no longer present; refresh before mutating")
	}
	if !sameTUIUserObservation(current, operation.User) {
		return "", errors.New("panel user changed after observation; refresh before mutating")
	}

	endpoint := "/api/users/" + strconv.FormatInt(operation.User.ID, 10)
	if operation.Action == "delete" {
		if _, err := client.request(ctx, http.MethodDelete, endpoint, nil, true); err != nil {
			return "", err
		}
		return "Deleted panel user " + operation.User.Email, nil
	}
	if operation.Action == "profile" {
		email, err := validatePanelUserEmail(operation.DesiredUser.Email)
		if err != nil {
			return "", err
		}
		name, err := validatePanelUserText("name", operation.DesiredUser.Name, 100, true)
		if err != nil {
			return "", err
		}
		payload := make(map[string]string, 2)
		if email != current.Email {
			payload["email"] = email
		}
		if name != current.Name {
			payload["name"] = name
		}
		if len(payload) == 0 {
			return "", errors.New("panel user already has the requested profile")
		}
		updated, err := requestJSON[models.User](ctx, client, http.MethodPut, endpoint, payload, true)
		if err != nil {
			return "", err
		}
		expected := current
		expected.Email, expected.Name = email, name
		if !validTUIUserUpdateReceipt(updated, expected, current.UpdatedAt) {
			return "", errors.New("panel-user profile update returned a mismatched receipt")
		}
		return "Updated panel user " + updated.Email, nil
	}
	if operation.Action == "password" {
		password, err := validatePanelUserPassword(operation.UserPassword)
		if err != nil {
			return "", err
		}
		updated, err := requestJSON[models.User](ctx, client, http.MethodPut, endpoint, map[string]string{"password": password}, true)
		if err != nil {
			return "", err
		}
		if !validTUIUserUpdateReceipt(updated, current, current.UpdatedAt) {
			return "", errors.New("panel-user password update returned a mismatched receipt")
		}
		return "Replaced password for " + current.Email, nil
	}
	roleText, found := strings.CutPrefix(operation.Action, "role-")
	if !found {
		return "", fmt.Errorf("unsupported panel-user TUI action %q", operation.Action)
	}
	role, err := validatePanelUserRole(roleText)
	if err != nil {
		return "", err
	}
	if role == string(current.Role) {
		return "", errors.New("panel user already has the requested role")
	}
	updated, err := requestJSON[models.User](ctx, client, http.MethodPut, endpoint, map[string]string{"role": role}, true)
	if err != nil {
		return "", err
	}
	expected := current
	expected.Role = models.Role(role)
	if !validTUIUserUpdateReceipt(updated, expected, current.UpdatedAt) {
		return "", errors.New("panel-user update returned a mismatched receipt")
	}
	return fmt.Sprintf("Changed %s role to %s", current.Email, role), nil
}

func validTUIUserUpdateReceipt(updated, expected models.User, previousUpdate time.Time) bool {
	return updated.ID == expected.ID && updated.Email == expected.Email && updated.Name == expected.Name &&
		updated.Role == expected.Role && updated.TOTPEnabled == expected.TOTPEnabled &&
		updated.CreatedAt.Equal(expected.CreatedAt) && !updated.UpdatedAt.Before(previousUpdate)
}

func findTUIUser(users []models.User, id int64) (models.User, bool) {
	for _, user := range users {
		if user.ID == id {
			return user, true
		}
	}
	return models.User{}, false
}

func sameTUIUserObservation(left, right models.User) bool {
	return left.ID == right.ID && left.Email == right.Email && left.Name == right.Name &&
		left.Role == right.Role && left.TOTPEnabled == right.TOTPEnabled &&
		left.CreatedAt.Equal(right.CreatedAt) && left.UpdatedAt.Equal(right.UpdatedAt)
}

func (model tuiModel) renderUsers(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Panel users") + tuiMutedStyle.Render("  I jump · a create · Enter manage · R reload")}
	if !model.usersLoaded {
		message := "Central panel-user inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading central panel-user inventory…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	if !model.users.Local || !model.users.Supported {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+valueOrNA(model.users.Message)))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	adminCount := 0
	for _, user := range model.users.Users {
		if user.Role == models.RoleAdmin {
			adminCount++
		}
	}
	rows = append(rows, tuiDimStyle.Render(fmt.Sprintf(
		"%d loaded · %d total · %d administrator(s) · central panel scope",
		len(model.users.Users), model.users.Total, adminCount,
	)))
	visible := maxInt(3, height-5)
	start, end := visibleRange(model.cursor, len(model.users.Users), visible)
	for index := start; index < end; index++ {
		user := model.users.Users[index]
		role := strings.ToUpper(string(user.Role))
		totp := "TOTP off"
		if user.TOTPEnabled {
			totp = "TOTP on"
		}
		current := ""
		if user.ID == model.users.CurrentUserID {
			current = " · current"
		}
		row := fmt.Sprintf("%-5d  %-10s  %-9s  %-24s  %s%s",
			user.ID, role, totp, truncateTUI(user.Name, 24), user.Email, current)
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	if len(model.users.Users) == 0 {
		rows = append(rows, tuiDimStyle.Render("No panel users were returned."))
	}
	if model.users.Message != "" {
		rows = append(rows, tuiDimStyle.Render(truncateTUI(model.users.Message, width-4)))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderUserFormDialog(width, height int) string {
	dialogWidth := minInt(88, width-4)
	if dialogWidth < 50 {
		dialogWidth = 50
	}
	form := model.dialog.UserForm
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, dialogWidth-6)),
	}
	for _, body := range model.dialog.Body {
		lines = append(lines, tuiMutedStyle.Render(truncateTUI(body, dialogWidth-6)))
	}
	lines = append(lines, "")
	for index, field := range form.Fields {
		value := field.Value
		placeholder := false
		if field.Secret && value != "" {
			value = strings.Repeat("•", utf8.RuneCountInString(value))
		}
		if value == "" {
			value = field.Placeholder
			placeholder = true
		}
		labelStyle := tuiMutedStyle
		valueStyle := lipgloss.NewStyle().Foreground(tuiText).Background(lipgloss.Color("#18181B"))
		marker := "  "
		if index == form.Cursor {
			labelStyle = lipgloss.NewStyle().Bold(true).Foreground(tuiAccentBright)
			valueStyle = valueStyle.Foreground(tuiAccentBright)
			marker = "› "
			value += "▏"
		} else if placeholder {
			valueStyle = valueStyle.Foreground(tuiMuted)
		}
		label := marker + fmt.Sprintf("%-14s", truncateTUI(field.Label, 14))
		available := maxInt(16, dialogWidth-26)
		renderedValue := truncateTUIInput(value, available-2)
		if index == form.Cursor {
			renderedValue = truncateTUIInputTail(value, available-2)
		}
		lines = append(lines, labelStyle.Render(label)+" "+valueStyle.Width(available).Render(renderedValue))
	}
	if form.Error != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(tuiRed).Render("! "+truncateTUI(form.Error, dialogWidth-8)))
	}
	lines = append(lines, "",
		tuiKeyStyle.Render("Tab / ↑↓")+tuiDimStyle.Render(" field   ")+
			tuiKeyStyle.Render("←/→")+tuiDimStyle.Render(" role   ")+
			tuiKeyStyle.Render("Enter")+tuiDimStyle.Render(" validate and review   ")+
			tuiKeyStyle.Render("Ctrl+U")+tuiDimStyle.Render(" clear   ")+
			tuiKeyStyle.Render("Esc")+tuiDimStyle.Render(" cancel"),
		tuiMutedStyle.Render("Secrets are masked; no mutation runs until a separate Y confirmation."),
	)
	box := lipgloss.NewStyle().Width(dialogWidth).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(1, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
