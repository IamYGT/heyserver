package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/notify"
	"github.com/IamYGT/heyserver/internal/store"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestHandleNotifyChannelList_EmptyChannels200(t *testing.T) {
	deps := contractTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/api/notify/channels", nil)
	rec := httptest.NewRecorder()

	handleNotifyChannelList(deps.ChannelRepo)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var channels []models.NotificationChannel
	testutil.ParseJSON(t, rec, &channels)
	if len(channels) != 0 {
		t.Errorf("channels len = %d, want 0", len(channels))
	}
}

func TestHandleNotifyChannelListAddsCanonicalAvailabilityWithoutChangingArrayShape(t *testing.T) {
	deps := contractTestDeps(t)
	channel := &models.NotificationChannel{
		Name:    "Unprobed Slack",
		Type:    models.ChannelSlack,
		Config:  `{"webhook_url":"https://hooks.example.com/abc"}`,
		Enabled: true,
	}
	if err := deps.ChannelRepo.Create(channel); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deps.ChannelRepo.Delete(channel.ID) })

	req := httptest.NewRequest(http.MethodGet, "/api/notify/channels", nil)
	rec := httptest.NewRecorder()
	handleNotifyChannelList(deps.ChannelRepo)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var channels []struct {
		ID     int64  `json:"id"`
		State  string `json:"state"`
		Detail string `json:"detail"`
	}
	testutil.ParseJSON(t, rec, &channels)
	if len(channels) != 1 || channels[0].ID != channel.ID {
		t.Fatalf("channels = %#v, want one channel with id %d", channels, channel.ID)
	}
	if channels[0].State != string(integrationstate.Unavailable) || channels[0].Detail != "probe_unverified" {
		t.Fatalf("availability = %#v, want unavailable/probe_unverified", channels[0])
	}
}

func TestHandleNotifyChannelListUsesFreshDeliveryReceipt(t *testing.T) {
	deps := contractTestDeps(t)
	channel := &models.NotificationChannel{
		Name:    "Verified Slack",
		Type:    models.ChannelSlack,
		Config:  `{"webhook_url":"https://hooks.example.com/verified"}`,
		Enabled: true,
	}
	if err := deps.ChannelRepo.Create(channel); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deps.ChannelRepo.Delete(channel.ID) })
	receipt := &models.NotificationDeliveryReceipt{
		ChannelID:             channel.ID,
		ChannelConfigRevision: channel.ConfigRevision,
		Outcome:               models.NotificationDeliveryOutcomeSuccess,
		Source:                models.NotificationDeliverySourceManualTest,
		ObservedAt:            time.Now().UTC(),
	}
	if err := deps.DeliveryRepo.Upsert(receipt); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/notify/channels", nil)
	rec := httptest.NewRecorder()
	handleNotifyChannelListWithReceipts(deps.ChannelRepo, deps.DeliveryRepo)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var channels []struct {
		ID     int64  `json:"id"`
		State  string `json:"state"`
		Detail string `json:"detail"`
	}
	testutil.ParseJSON(t, rec, &channels)
	if len(channels) != 1 || channels[0].ID != channel.ID {
		t.Fatalf("channels = %#v, want verified channel %d", channels, channel.ID)
	}
	if channels[0].State != string(integrationstate.Healthy) || channels[0].Detail != "delivery_confirmed" {
		t.Fatalf("availability = %#v, want healthy/delivery_confirmed", channels[0])
	}
}

func TestHandleNotifyChannelTestPersistsSuccessAndFailureReceipts(t *testing.T) {
	for _, test := range []struct {
		name       string
		outcome    models.NotificationDeliveryOutcome
		sendErr    error
		wantStatus int
	}{
		{name: "success", outcome: models.NotificationDeliveryOutcomeSuccess, wantStatus: http.StatusOK},
		{name: "failure", outcome: models.NotificationDeliveryOutcomeFailure, sendErr: errors.New("provider detail must not persist"), wantStatus: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps := contractTestDeps(t)
			channel := &models.NotificationChannel{Name: "Receipt test", Type: models.ChannelSlack, Config: `{"webhook_url":"https://hooks.example.com/test"}`, Enabled: true}
			if err := deps.ChannelRepo.Create(channel); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = deps.ChannelRepo.Delete(channel.ID) })
			sender := func(ch models.NotificationChannel) (notify.DeliveryResult, error) {
				return notify.DeliveryResult{ChannelID: ch.ID, ChannelConfigRevision: ch.ConfigRevision, Outcome: test.outcome}, test.sendErr
			}
			req := httptest.NewRequest(http.MethodPost, "/api/notify/channels/1/test", nil)
			req.SetPathValue("id", strconv.FormatInt(channel.ID, 10))
			rec := httptest.NewRecorder()
			handleNotifyChannelTestWithSender(deps.ChannelRepo, deps.DeliveryRepo, sender)(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, test.wantStatus, rec.Body.String())
			}
			receipt, err := deps.DeliveryRepo.Get(channel.ID)
			if err != nil || receipt == nil {
				t.Fatalf("delivery receipt: receipt=%+v err=%v", receipt, err)
			}
			if receipt.Outcome != test.outcome || receipt.Source != models.NotificationDeliverySourceManualTest || receipt.ChannelConfigRevision != channel.ConfigRevision {
				t.Fatalf("delivery receipt = %+v", receipt)
			}
			if strings.Contains(rec.Body.String(), "provider detail") {
				t.Fatal("response exposed provider error detail")
			}
		})
	}
}

func TestHandleNotifyChannelList_NilRepo503(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/notify/channels", nil)
	rec := httptest.NewRecorder()
	handleNotifyChannelList(nil)(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body map[string]string
	testutil.ParseJSON(t, rec, &body)
	if body["state"] != string(integrationstate.Unavailable) {
		t.Fatalf("error state = %#v, want unavailable", body)
	}
}

func TestIntegration_NotifyChannelCreate(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "mgr@test.com", models.RoleManager)
	body := `{"name":"Pager","type":"email","config":"{\"to\":\"ops@example.com\"}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/notify/channels", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testutil.MakeToken(t, user))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%s", rec.Code, rec.Body.String())
	}
	var ch models.NotificationChannel
	testutil.ParseJSON(t, rec, &ch)
	if ch.ID == 0 || ch.Name != "Pager" {
		t.Errorf("channel = %+v", ch)
	}
	repo, err := store.NewNotificationChannelRepository(db.Instance(), contractTestDataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Delete(ch.ID) })
}

func TestIntegration_NotifyChannelSecretsAreRedactedAndBlankUpdatePreservesThem(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "notify-manager@test.com", models.RoleManager)
	token := testutil.MakeToken(t, user)
	secret := "123456:notification-unit-test-secret"
	body := `{"name":"Protected Telegram","type":"telegram","config":"{\"bot_token\":\"` + secret + `\",\"chat_id\":\"-1001\"}"}`
	request := httptest.NewRequest(http.MethodPost, "/api/notify/channels", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	createdRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createdRecorder, request)
	if createdRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createdRecorder.Code, createdRecorder.Body.String())
	}
	if strings.Contains(createdRecorder.Body.String(), secret) {
		t.Fatal("create response exposed notification secret")
	}
	var created models.NotificationChannel
	testutil.ParseJSON(t, createdRecorder, &created)
	var publicConfig map[string]any
	if err := json.Unmarshal([]byte(created.Config), &publicConfig); err != nil {
		t.Fatal(err)
	}
	if publicConfig["secret_configured"] != true || publicConfig["chat_id"] != "-1001" {
		t.Fatalf("redacted create config = %#v", publicConfig)
	}

	updateBody := `{"name":"Protected Telegram Updated","type":"telegram","config":"{\"bot_token\":\"\",\"chat_id\":\"-2002\"}"}`
	updateRequest := httptest.NewRequest(http.MethodPut, "/api/notify/channels/"+strconv.FormatInt(created.ID, 10), strings.NewReader(updateBody))
	updateRequest.Header.Set("Authorization", "Bearer "+token)
	updateRequest.Header.Set("Content-Type", "application/json")
	updatedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(updatedRecorder, updateRequest)
	if updatedRecorder.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", updatedRecorder.Code, updatedRecorder.Body.String())
	}
	if strings.Contains(updatedRecorder.Body.String(), secret) {
		t.Fatal("update response exposed notification secret")
	}

	repo, err := store.NewNotificationChannelRepository(db.Instance(), contractTestDataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Delete(created.ID) })
	stored, err := repo.Get(created.ID)
	if err != nil || stored == nil {
		t.Fatalf("stored channel: err=%v channel=%+v", err, stored)
	}
	var telegram models.TelegramConfig
	if err := json.Unmarshal([]byte(stored.Config), &telegram); err != nil {
		t.Fatal(err)
	}
	if telegram.BotToken != secret || telegram.ChatID != -2002 {
		t.Fatalf("protected update config = %+v", telegram)
	}

	clearBody := `{"name":"Protected Telegram Updated","type":"telegram","config":"{\"bot_token\":\"\",\"chat_id\":\"-2002\"}","clearSecret":true}`
	clearRequest := httptest.NewRequest(http.MethodPut, "/api/notify/channels/"+strconv.FormatInt(created.ID, 10), strings.NewReader(clearBody))
	clearRequest.Header.Set("Authorization", "Bearer "+token)
	clearRequest.Header.Set("Content-Type", "application/json")
	clearRecorder := httptest.NewRecorder()
	handler.ServeHTTP(clearRecorder, clearRequest)
	if clearRecorder.Code != http.StatusOK {
		t.Fatalf("clear status = %d body=%s", clearRecorder.Code, clearRecorder.Body.String())
	}
	var clearedPublic models.NotificationChannel
	testutil.ParseJSON(t, clearRecorder, &clearedPublic)
	if strings.Contains(clearedPublic.Config, secret) || !strings.Contains(clearedPublic.Config, `"secret_configured":false`) {
		t.Fatalf("cleared public config = %s", clearedPublic.Config)
	}
	clearedStored, err := repo.Get(created.ID)
	if err != nil || clearedStored == nil {
		t.Fatalf("cleared stored channel: err=%v channel=%+v", err, clearedStored)
	}
	if err := json.Unmarshal([]byte(clearedStored.Config), &telegram); err != nil {
		t.Fatal(err)
	}
	if telegram.BotToken != "" {
		t.Fatalf("cleared protected config retained token: %+v", telegram)
	}
}

func TestIntegration_MetricsHistoryWithDeps(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/metrics/history?duration=1h", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}
