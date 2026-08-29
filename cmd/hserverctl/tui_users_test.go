package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

func TestLoadTUIUsersUsesCentralBoundaryAndValidatesInventory(t *testing.T) {
	t.Parallel()
	users := testTUIUsers()
	current := users[0]
	current.ID = 999 // The authenticated account can be older than the newest loaded page.
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/auth/me":
			_ = json.NewEncoder(writer).Encode(current)
		case "GET /api/users":
			if request.URL.RawQuery != "limit=200&offset=0" {
				t.Errorf("query = %q", request.URL.RawQuery)
			}
			_ = json.NewEncoder(writer).Encode(tuiUserListResponse{Data: users, Total: 204, Limit: 200})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	state, err := loadTUIUsers(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !state.Local || !state.Supported || state.CurrentUserID != current.ID || state.Total != 204 || !reflect.DeepEqual(state.Users, users) {
		t.Fatalf("state = %#v", state)
	}
	if !strings.Contains(state.Message, "newest 2 of 204") {
		t.Fatalf("pagination message = %q", state.Message)
	}
	before := requests.Load()
	managed, err := loadTUIUsers(context.Background(), client, tuiTarget{ID: "edge-1", Name: "Edge", Online: true})
	if err != nil {
		t.Fatal(err)
	}
	if managed.Local || managed.Supported || !strings.Contains(managed.Message, "central control plane") {
		t.Fatalf("managed state = %#v", managed)
	}
	if requests.Load() != before {
		t.Fatal("managed user boundary made a central user API request")
	}
}

func TestTUIUsersJumpActionsRenderAndPalette(t *testing.T) {
	t.Parallel()
	users := testTUIUsers()
	target := initialTUITargets()[0]
	model := newTUIModel(context.Background(), nil, "http://127.0.0.1", 5*time.Second)
	model.loading = false
	model.snapshot.Selected = target
	model.users = tuiUsersState{Local: true, Supported: true, Users: users, CurrentUserID: users[0].ID, Total: len(users)}
	model.usersTarget, model.usersLoaded = localTargetID, true

	updated, command := model.updateKey("I")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabUsers {
		t.Fatalf("Users jump tab=%v command=%v", model.tab, command != nil)
	}
	view := model.View().Content
	for _, expected := range []string{"Panel users", "ADMIN", "TOTP on", users[0].Name, users[0].Email, "current", users[1].Email} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view missing %q: %q", expected, view)
		}
	}

	updated, command = model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || userDialogHasAction(model.dialog, "delete") {
		t.Fatalf("current-user choices = %#v", model.dialog)
	}
	model.dialog = tuiDialog{}
	model.cursor = 1
	updated, command = model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || !userDialogHasAction(model.dialog, "delete") {
		t.Fatalf("other-user choices = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Kind != tuiOperationUser || model.dialog.Operation.Action != "role-admin" {
		t.Fatalf("role confirmation = %#v", model.dialog)
	}

	foundSection, foundCreate, foundRole, foundDeleteOther, foundDeleteCurrent := false, false, false, false, false
	for _, item := range model.buildPaletteItems() {
		foundSection = foundSection || item.Kind == tuiPaletteNavigate && item.Tab == tuiTabUsers
		foundCreate = foundCreate || item.Kind == tuiPaletteUserCreate
		if item.Kind != tuiPaletteOperation || item.Operation.Kind != tuiOperationUser {
			continue
		}
		foundRole = foundRole || item.Operation.User.ID == users[1].ID && item.Operation.Action == "role-admin"
		foundDeleteOther = foundDeleteOther || item.Operation.User.ID == users[1].ID && item.Operation.Action == "delete"
		foundDeleteCurrent = foundDeleteCurrent || item.Operation.User.ID == users[0].ID && item.Operation.Action == "delete"
	}
	if !foundSection || !foundCreate || !foundRole || !foundDeleteOther || foundDeleteCurrent {
		t.Fatalf("palette section=%t create=%t role=%t deleteOther=%t deleteCurrent=%t", foundSection, foundCreate, foundRole, foundDeleteOther, foundDeleteCurrent)
	}
}

func TestTUIUserFormsMaskSecretsAndRequireConfirmation(t *testing.T) {
	t.Parallel()
	users := testTUIUsers()
	target := initialTUITargets()[0]
	model := newTUIModel(context.Background(), nil, "http://127.0.0.1", 5*time.Second)
	model.loading = false
	model.snapshot.Selected = target
	model.users = tuiUsersState{Local: true, Supported: true, Users: users, CurrentUserID: users[0].ID, Total: len(users)}
	model.usersTarget, model.usersLoaded = localTargetID, true
	model.tab = tuiTabUsers

	updated, command := model.updateKey("a")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogUserForm || model.dialog.UserForm.Kind != tuiUserFormCreate {
		t.Fatalf("create form mode=%v kind=%q command=%v", model.dialog.Mode, model.dialog.UserForm.Kind, command != nil)
	}
	model.dialog.UserForm.Cursor = userFormFieldIndex(t, model.dialog.UserForm, "name")
	updated, command = model.updateDialogKey("space")
	model = updated.(tuiModel)
	if command != nil || model.dialog.UserForm.rawValue("name") != " " {
		t.Fatalf("name space input = %q command=%v", model.dialog.UserForm.rawValue("name"), command != nil)
	}
	model.dialog.UserForm.Cursor = userFormFieldIndex(t, model.dialog.UserForm, "role")
	updated, command = model.updateDialogKey("right")
	model = updated.(tuiModel)
	if command != nil || model.dialog.UserForm.rawValue("role") != "admin" {
		t.Fatalf("role selector = %q command=%v", model.dialog.UserForm.rawValue("role"), command != nil)
	}
	setTUIUserFormValue(t, &model.dialog.UserForm, "email", "new@example.com")
	setTUIUserFormValue(t, &model.dialog.UserForm, "name", "New Operator")
	setTUIUserFormValue(t, &model.dialog.UserForm, "role", "manager")
	setTUIUserFormValue(t, &model.dialog.UserForm, "password", "correct horse battery staple")
	setTUIUserFormValue(t, &model.dialog.UserForm, "confirm", "correct horse battery staple")
	if view := model.View().Content; strings.Contains(view, "correct horse battery staple") || !strings.Contains(view, "••••") {
		t.Fatalf("masked create form = %q", view)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "create" || model.dialog.Operation.DesiredUser.Email != "new@example.com" {
		t.Fatalf("create confirmation mode=%v action=%q command=%v", model.dialog.Mode, model.dialog.Operation.Action, command != nil)
	}
	if view := model.View().Content; strings.Contains(view, "correct horse battery staple") {
		t.Fatal("create confirmation rendered the password")
	}

	model.dialog = tuiDialog{}
	model.openUserActions(users[1])
	model.dialog.Cursor = userDialogActionIndex(t, model.dialog, "edit-profile")
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogUserForm || model.dialog.UserForm.Kind != tuiUserFormProfile {
		t.Fatalf("profile form = %#v", model.dialog.UserForm)
	}
	setTUIUserFormValue(t, &model.dialog.UserForm, "name", "Support Operator")
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "profile" || model.dialog.Operation.DesiredUser.Name != "Support Operator" {
		t.Fatalf("profile confirmation action=%q desired=%q", model.dialog.Operation.Action, model.dialog.Operation.DesiredUser.Name)
	}

	model.dialog = tuiDialog{}
	model.openUserActions(users[1])
	model.dialog.Cursor = userDialogActionIndex(t, model.dialog, "replace-password")
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogUserForm || model.dialog.UserForm.Kind != tuiUserFormPassword {
		t.Fatalf("password form mode=%v kind=%q", model.dialog.Mode, model.dialog.UserForm.Kind)
	}
	setTUIUserFormValue(t, &model.dialog.UserForm, "password", "replacement-secret")
	setTUIUserFormValue(t, &model.dialog.UserForm, "confirm", "replacement-secret")
	if view := model.View().Content; strings.Contains(view, "replacement-secret") {
		t.Fatal("password form rendered the password")
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "password" || !model.dialog.Operation.Dangerous {
		t.Fatalf("password confirmation action=%q dangerous=%t", model.dialog.Operation.Action, model.dialog.Operation.Dangerous)
	}
	if view := model.View().Content; strings.Contains(view, "replacement-secret") {
		t.Fatal("password confirmation rendered the password")
	}
}

func TestRunTUIUserOperationReobservesAndValidatesReceipt(t *testing.T) {
	t.Parallel()
	observed := testTUIUsers()[1]
	var inventoryRequests, updateRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/users":
			inventoryRequests.Add(1)
			_ = json.NewEncoder(writer).Encode(tuiUserListResponse{Data: []models.User{observed}, Total: 1, Limit: 200})
		case "PUT /api/users/22":
			updateRequests.Add(1)
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(payload, map[string]string{"role": "manager"}) {
				t.Errorf("payload = %#v", payload)
			}
			updated := observed
			updated.Role = models.RoleManager
			updated.UpdatedAt = updated.UpdatedAt.Add(time.Minute)
			_ = json.NewEncoder(writer).Encode(updated)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	message, err := runTUIUserOperation(context.Background(), client, tuiOperation{
		Target: initialTUITargets()[0], Action: "role-manager", User: observed, CurrentUserID: 11,
	})
	if err != nil || message != "Changed viewer@example.com role to manager" || inventoryRequests.Load() != 1 || updateRequests.Load() != 1 {
		t.Fatalf("message=%q err=%v inventory=%d updates=%d", message, err, inventoryRequests.Load(), updateRequests.Load())
	}
}

func TestRunTUIUserOperationRejectsStaleAndCurrentDeletion(t *testing.T) {
	t.Parallel()
	observed := testTUIUsers()[1]
	stale := observed
	stale.UpdatedAt = stale.UpdatedAt.Add(time.Second)
	var requests, mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/users" {
			_ = json.NewEncoder(writer).Encode(tuiUserListResponse{Data: []models.User{stale}, Total: 1, Limit: 200})
			return
		}
		mutations.Add(1)
		http.Error(writer, "unexpected mutation", http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runTUIUserOperation(context.Background(), client, tuiOperation{Target: initialTUITargets()[0], Action: "role-manager", User: observed})
	if err == nil || !strings.Contains(err.Error(), "changed after observation") || mutations.Load() != 0 {
		t.Fatalf("stale err=%v mutations=%d", err, mutations.Load())
	}
	before := requests.Load()
	_, err = runTUIUserOperation(context.Background(), client, tuiOperation{Target: initialTUITargets()[0], Action: "delete", User: observed, CurrentUserID: observed.ID})
	if err == nil || !strings.Contains(err.Error(), "current panel account") || requests.Load() != before {
		t.Fatalf("current-delete err=%v requests=%d before=%d", err, requests.Load(), before)
	}
}

func TestRunTUIUserDeleteReobservesAndAcceptsEmptyReceipt(t *testing.T) {
	t.Parallel()
	observed := testTUIUsers()[1]
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/users":
			_ = json.NewEncoder(writer).Encode(tuiUserListResponse{Data: []models.User{observed}, Total: 1, Limit: 200})
		case "DELETE /api/users/22":
			deletes.Add(1)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) != 0 {
				t.Errorf("delete body = %q", body)
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	message, err := runTUIUserOperation(context.Background(), client, tuiOperation{
		Target: initialTUITargets()[0], Action: "delete", User: observed, CurrentUserID: 11,
	})
	if err != nil || message != "Deleted panel user viewer@example.com" || deletes.Load() != 1 {
		t.Fatalf("message=%q err=%v deletes=%d", message, err, deletes.Load())
	}
}

func TestRunTUIUserCreateProfileAndPasswordUseExactPayloads(t *testing.T) {
	t.Parallel()
	observed := testTUIUsers()[1]
	current := observed
	var creates, profileUpdates, passwordUpdates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /api/users":
			creates.Add(1)
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"email": "new@example.com", "name": "New Operator", "role": "manager", "password": "correct horse battery staple"}
			if !reflect.DeepEqual(payload, want) {
				t.Errorf("create payload = %#v", payload)
			}
			created := models.User{ID: 33, Email: want["email"], Name: want["name"], Role: models.RoleManager, CreatedAt: observed.CreatedAt, UpdatedAt: observed.UpdatedAt}
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(created)
		case "GET /api/users":
			_ = json.NewEncoder(writer).Encode(tuiUserListResponse{Data: []models.User{current}, Total: 1, Limit: 200})
		case "PUT /api/users/22":
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			switch {
			case payload["name"] != "":
				profileUpdates.Add(1)
				if !reflect.DeepEqual(payload, map[string]string{"name": "Support Operator"}) {
					t.Errorf("profile payload = %#v", payload)
				}
				current.Name = payload["name"]
				current.UpdatedAt = current.UpdatedAt.Add(time.Minute)
			case payload["password"] != "":
				passwordUpdates.Add(1)
				if !reflect.DeepEqual(payload, map[string]string{"password": "replacement-secret"}) {
					t.Errorf("password payload keys = %#v", payload)
				}
				current.UpdatedAt = current.UpdatedAt.Add(time.Minute)
			default:
				t.Errorf("unexpected update payload = %#v", payload)
			}
			_ = json.NewEncoder(writer).Encode(current)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := initialTUITargets()[0]
	message, err := runTUIUserOperation(context.Background(), client, tuiOperation{
		Target: target, Action: "create",
		DesiredUser:  models.User{Email: "new@example.com", Name: "New Operator", Role: models.RoleManager},
		UserPassword: "correct horse battery staple",
	})
	if err != nil || message != "Created panel user new@example.com" {
		t.Fatalf("create message=%q err=%v", message, err)
	}

	desired := observed
	desired.Name = "Support Operator"
	message, err = runTUIUserOperation(context.Background(), client, tuiOperation{
		Target: target, Action: "profile", User: observed, DesiredUser: desired,
	})
	if err != nil || message != "Updated panel user viewer@example.com" {
		t.Fatalf("profile message=%q err=%v", message, err)
	}

	profiled := current
	message, err = runTUIUserOperation(context.Background(), client, tuiOperation{
		Target: target, Action: "password", User: profiled, UserPassword: "replacement-secret",
	})
	if err != nil || message != "Replaced password for viewer@example.com" {
		t.Fatalf("password message=%q err=%v", message, err)
	}
	if creates.Load() != 1 || profileUpdates.Load() != 1 || passwordUpdates.Load() != 1 {
		t.Fatalf("creates=%d profiles=%d passwords=%d", creates.Load(), profileUpdates.Load(), passwordUpdates.Load())
	}
}

func TestTUIUserFormRejectsInvalidEmailAndPasswordMismatch(t *testing.T) {
	t.Parallel()
	target := initialTUITargets()[0]
	form := tuiUserForm{Kind: tuiUserFormCreate, Fields: []tuiUserFormField{
		{Key: "email", Value: "not-an-email"},
		{Key: "name", Value: "Operator"},
		{Key: "role", Value: "viewer"},
		{Key: "password", Value: "valid-password"},
		{Key: "confirm", Value: "valid-password"},
	}}
	if _, err := operationFromTUIUserForm(form, target); err == nil || !strings.Contains(err.Error(), "invalid format") {
		t.Fatalf("invalid email err=%v", err)
	}
	form.Fields[0].Value = "operator@example.com"
	form.Fields[4].Value = "different-password"
	if _, err := operationFromTUIUserForm(form, target); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("password confirmation err=%v", err)
	}
}

func testTUIUsers() []models.User {
	created := time.Date(2026, time.August, 28, 8, 0, 0, 0, time.UTC)
	return []models.User{
		{ID: 11, Email: "admin@example.com", Name: "Admin Operator", Role: models.RoleAdmin, TOTPEnabled: true, CreatedAt: created, UpdatedAt: created.Add(time.Minute)},
		{ID: 22, Email: "viewer@example.com", Name: "Read Only", Role: models.RoleViewer, CreatedAt: created.Add(time.Hour), UpdatedAt: created.Add(time.Hour + time.Minute)},
	}
}

func userDialogHasAction(dialog tuiDialog, action string) bool {
	for _, option := range dialog.Options {
		if option.Action == action {
			return true
		}
	}
	return false
}

func userDialogActionIndex(t *testing.T, dialog tuiDialog, action string) int {
	t.Helper()
	for index, option := range dialog.Options {
		if option.Action == action {
			return index
		}
	}
	t.Fatalf("dialog does not contain action %q", action)
	return 0
}

func setTUIUserFormValue(t *testing.T, form *tuiUserForm, key, value string) {
	t.Helper()
	for index := range form.Fields {
		if form.Fields[index].Key == key {
			form.Fields[index].Value = value
			return
		}
	}
	t.Fatalf("user form does not contain field %q", key)
}

func userFormFieldIndex(t *testing.T, form tuiUserForm, key string) int {
	t.Helper()
	for index, field := range form.Fields {
		if field.Key == key {
			return index
		}
	}
	t.Fatalf("user form does not contain field %q", key)
	return 0
}
