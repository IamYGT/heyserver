package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/notify"
	"github.com/IamYGT/heyserver/internal/store"
)

type notificationChannelMutationRequest struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Config      string `json:"config"`
	Enabled     *bool  `json:"enabled,omitempty"`
	ClearSecret bool   `json:"clearSecret,omitempty"`
}

type alertRuleMutationRequest struct {
	Name         *string  `json:"name,omitempty"`
	Type         *string  `json:"type,omitempty"`
	Threshold    *float64 `json:"threshold,omitempty"`
	DurationMins *int     `json:"durationMins,omitempty"`
	Target       *string  `json:"target,omitempty"`
	Enabled      *bool    `json:"enabled,omitempty"`
	CooldownMins *int     `json:"cooldownMins,omitempty"`
}

const notificationIntegrationStateHeader = "X-HServer-Integration-State"

func (req alertRuleMutationRequest) empty() bool {
	return req.Name == nil && req.Type == nil && req.Threshold == nil &&
		req.DurationMins == nil && req.Target == nil && req.Enabled == nil && req.CooldownMins == nil
}

func (req alertRuleMutationRequest) apply(rule *models.AlertRule) {
	if req.Name != nil {
		rule.Name = *req.Name
	}
	if req.Type != nil {
		rule.Type = *req.Type
	}
	if req.Threshold != nil {
		rule.Threshold = *req.Threshold
	}
	if req.DurationMins != nil {
		rule.DurationMins = *req.DurationMins
	}
	if req.Target != nil {
		rule.Target = *req.Target
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if req.CooldownMins != nil {
		rule.CooldownMins = *req.CooldownMins
	}
}

func notificationRepositoryError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotificationSecretStoreUnavailable) {
		notificationStateError(w, http.StatusServiceUnavailable, "notification protected config store is unavailable")
		return
	}
	notificationStateError(w, http.StatusInternalServerError, err.Error())
}

// notificationStateError keeps the existing error status/message contract and
// adds the canonical optional-integration state for clients that can render a
// truthful remediation surface.
func notificationStateError(w http.ResponseWriter, status int, message string) {
	w.Header().Set(notificationIntegrationStateHeader, string(integrationstate.Unavailable))
	jsonResponse(w, status, map[string]string{
		"error": message,
		"state": string(integrationstate.Unavailable),
	})
}

// publicNotificationChannelState is intentionally an additive API view. The
// embedded model keeps GET /notify/channels an array of channel objects for
// existing clients while exposing the canonical delivery state beside each
// redacted channel.
type publicNotificationChannelState struct {
	models.NotificationChannel
	State  integrationstate.State `json:"state"`
	Detail string                 `json:"detail,omitempty"`
}

func publicNotificationChannel(channel models.NotificationChannel) (models.NotificationChannel, error) {
	config, err := notify.RedactChannelConfig(channel.Type, channel.Config)
	if err != nil {
		return models.NotificationChannel{}, err
	}
	channel.Config = config
	return channel, nil
}

func publicNotificationChannelWithState(channel models.NotificationChannel) (publicNotificationChannelState, error) {
	return publicNotificationChannelWithReceipt(channel, nil, time.Now().UTC())
}

func publicNotificationChannelWithReceipt(channel models.NotificationChannel, receipt *models.NotificationDeliveryReceipt, now time.Time) (publicNotificationChannelState, error) {
	publicChannel, err := publicNotificationChannel(channel)
	if err != nil {
		return publicNotificationChannelState{}, err
	}
	availability := notify.ChannelAvailabilityWithReceipt(channel, receipt, now)
	return publicNotificationChannelState{
		NotificationChannel: publicChannel,
		State:               availability.State,
		Detail:              availability.Detail,
	}, nil
}

func handleNotifyChannelList(repo *store.NotificationChannelRepository) http.HandlerFunc {
	return handleNotifyChannelListWithReceipts(repo, nil)
}

func handleNotifyChannelListWithReceipts(repo *store.NotificationChannelRepository, deliveryRepo *store.NotificationDeliveryReceiptRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			notificationStateError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		channels, err := repo.List()
		if err != nil {
			notificationRepositoryError(w, err)
			return
		}
		receipts, err := notificationDeliveryReceiptMap(r.Context(), deliveryRepo)
		if err != nil {
			notificationRepositoryError(w, err)
			return
		}
		now := time.Now().UTC()
		availability := notify.ChannelsAvailabilityWithReceipts(channels, receipts, now)
		w.Header().Set(notificationIntegrationStateHeader, string(availability.State))
		publicChannels := make([]publicNotificationChannelState, 0, len(channels))
		for _, channel := range channels {
			publicChannel, err := publicNotificationChannelWithReceipt(channel, receiptForChannel(receipts, channel.ID), now)
			if err != nil {
				notificationStateError(w, http.StatusServiceUnavailable, "notification channel config is unavailable")
				return
			}
			publicChannels = append(publicChannels, publicChannel)
		}
		jsonResponse(w, http.StatusOK, publicChannels)
	}
}

func notificationDeliveryReceiptMap(ctx context.Context, repo *store.NotificationDeliveryReceiptRepository) (map[int64]models.NotificationDeliveryReceipt, error) {
	out := make(map[int64]models.NotificationDeliveryReceipt)
	if repo == nil {
		return out, nil
	}
	receipts, err := repo.ListContext(ctx)
	if err != nil {
		return nil, err
	}
	for _, receipt := range receipts {
		out[receipt.ChannelID] = receipt
	}
	return out, nil
}

func receiptForChannel(receipts map[int64]models.NotificationDeliveryReceipt, channelID int64) *models.NotificationDeliveryReceipt {
	receipt, ok := receipts[channelID]
	if !ok {
		return nil
	}
	return &receipt
}

func handleNotifyChannelGet(repo *store.NotificationChannelRepository) http.HandlerFunc {
	return handleNotifyChannelGetWithReceipts(repo, nil)
}

func handleNotifyChannelGetWithReceipts(repo *store.NotificationChannelRepository, deliveryRepo *store.NotificationDeliveryReceiptRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			notificationStateError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		ch, err := repo.Get(id)
		if err != nil {
			notificationRepositoryError(w, err)
			return
		}
		if ch == nil {
			jsonError(w, http.StatusNotFound, "channel not found")
			return
		}
		var receipt *models.NotificationDeliveryReceipt
		if deliveryRepo != nil {
			receipt, err = deliveryRepo.GetContext(r.Context(), ch.ID)
			if err != nil {
				notificationRepositoryError(w, err)
				return
			}
		}
		publicChannel, err := publicNotificationChannelWithReceipt(*ch, receipt, time.Now().UTC())
		if err != nil {
			notificationStateError(w, http.StatusServiceUnavailable, "notification channel config is unavailable")
			return
		}
		w.Header().Set(notificationIntegrationStateHeader, string(publicChannel.State))
		jsonResponse(w, http.StatusOK, publicChannel)
	}
}

func handleNotifyChannelCreate(repo *store.NotificationChannelRepository) http.HandlerFunc {
	return handleNotifyChannelCreateWithReceipts(repo, nil)
}

func handleNotifyChannelCreateWithReceipts(repo *store.NotificationChannelRepository, deliveryRepo *store.NotificationDeliveryReceiptRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			notificationStateError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		var req notificationChannelMutationRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Type) == "" {
			jsonError(w, http.StatusBadRequest, "name and type required")
			return
		}
		if req.ClearSecret {
			jsonError(w, http.StatusBadRequest, "clearSecret is only valid when updating a channel")
			return
		}
		config, err := notify.NormalizeChannelConfig(req.Type, req.Config, `{}`)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		ch := models.NotificationChannel{Name: strings.TrimSpace(req.Name), Type: req.Type, Config: config}
		ch.Enabled = true
		if req.Enabled != nil {
			ch.Enabled = *req.Enabled
		}
		if err := repo.Create(&ch); err != nil {
			notificationRepositoryError(w, err)
			return
		}
		publicChannel, err := publicNotificationChannelWithReceipt(ch, nil, time.Now().UTC())
		if err != nil {
			notificationStateError(w, http.StatusServiceUnavailable, "notification channel config is unavailable")
			return
		}
		w.Header().Set(notificationIntegrationStateHeader, string(publicChannel.State))
		jsonResponse(w, http.StatusCreated, publicChannel)
	}
}

func handleNotifyChannelUpdate(repo *store.NotificationChannelRepository) http.HandlerFunc {
	return handleNotifyChannelUpdateWithReceipts(repo, nil)
}

func handleNotifyChannelUpdateWithReceipts(repo *store.NotificationChannelRepository, deliveryRepo *store.NotificationDeliveryReceiptRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			notificationStateError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		existing, err := repo.Get(id)
		if err != nil {
			notificationRepositoryError(w, err)
			return
		}
		if existing == nil {
			jsonError(w, http.StatusNotFound, "channel not found")
			return
		}
		var req notificationChannelMutationRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			jsonError(w, http.StatusBadRequest, "name required")
			return
		}
		if req.Type != "" && req.Type != existing.Type {
			jsonError(w, http.StatusBadRequest, "channel type cannot be changed")
			return
		}
		config, err := notify.NormalizeChannelConfig(existing.Type, req.Config, existing.Config)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.ClearSecret {
			config, err = notify.ClearChannelSecret(existing.Type, config)
			if err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		existing.ID = id
		existing.Name = strings.TrimSpace(req.Name)
		existing.Config = config
		if req.Enabled != nil {
			existing.Enabled = *req.Enabled
		}
		if err := repo.Update(existing); err != nil {
			notificationRepositoryError(w, err)
			return
		}
		var receipt *models.NotificationDeliveryReceipt
		if deliveryRepo != nil {
			receipt, err = deliveryRepo.GetContext(r.Context(), existing.ID)
			if err != nil {
				notificationRepositoryError(w, err)
				return
			}
		}
		publicChannel, err := publicNotificationChannelWithReceipt(*existing, receipt, time.Now().UTC())
		if err != nil {
			notificationStateError(w, http.StatusServiceUnavailable, "notification channel config is unavailable")
			return
		}
		w.Header().Set(notificationIntegrationStateHeader, string(publicChannel.State))
		jsonResponse(w, http.StatusOK, publicChannel)
	}
}

func handleNotifyChannelDelete(repo *store.NotificationChannelRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			notificationStateError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := repo.Delete(id); err != nil {
			notificationRepositoryError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

type manualNotificationSender func(models.NotificationChannel) (notify.DeliveryResult, error)

func handleNotifyChannelTest(repo *store.NotificationChannelRepository, deliveryRepo *store.NotificationDeliveryReceiptRepository) http.HandlerFunc {
	return handleNotifyChannelTestWithSender(repo, deliveryRepo, notify.FireManualWithResult)
}

func handleNotifyChannelTestWithSender(repo *store.NotificationChannelRepository, deliveryRepo *store.NotificationDeliveryReceiptRepository, sender manualNotificationSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil || deliveryRepo == nil || sender == nil {
			notificationStateError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		ch, err := repo.Get(id)
		if err != nil {
			notificationRepositoryError(w, err)
			return
		}
		if ch == nil {
			jsonError(w, http.StatusNotFound, "channel not found")
			return
		}
		result, sendErr := sender(*ch)
		completedAt := time.Now().UTC()
		receiptCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		receiptErr := notify.PersistDeliveryResults(receiptCtx, deliveryRepo, models.NotificationDeliverySourceManualTest, []notify.DeliveryResult{result}, completedAt)
		cancel()
		if sendErr != nil {
			notificationStateError(w, http.StatusBadGateway, "test delivery failed")
			return
		}
		if receiptErr != nil {
			notificationStateError(w, http.StatusServiceUnavailable, "test sent but delivery receipt could not be stored")
			return
		}
		w.Header().Set(notificationIntegrationStateHeader, string(integrationstate.Healthy))
		jsonResponse(w, http.StatusOK, map[string]string{
			"status": "sent",
			"state":  string(integrationstate.Healthy),
			"detail": "delivery_confirmed",
		})
	}
}

func handleAlertRuleList(repo *store.AlertRuleRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			jsonError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		rules, err := repo.List()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, rules)
	}
}

func handleAlertRuleGet(repo *store.AlertRuleRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			jsonError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		rule, err := repo.Get(id)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rule == nil {
			jsonError(w, http.StatusNotFound, "rule not found")
			return
		}
		jsonResponse(w, http.StatusOK, rule)
	}
}

func handleAlertRuleCreate(repo *store.AlertRuleRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			jsonError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		var req alertRuleMutationRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if req.Name == nil || req.Type == nil {
			jsonError(w, http.StatusBadRequest, "name and type required")
			return
		}
		if req.Threshold == nil && notify.NormalizeAlertType(*req.Type) != models.AlertServiceDown {
			jsonError(w, http.StatusBadRequest, "threshold required")
			return
		}
		rule := models.AlertRule{Enabled: true, CooldownMins: 15}
		req.apply(&rule)
		normalized, err := notify.ValidateAndNormalizeAlertRule(rule)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		rule = normalized
		if err := repo.Create(&rule); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, rule)
	}
}

func handleAlertRuleUpdate(repo *store.AlertRuleRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			jsonError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		existing, err := repo.Get(id)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if existing == nil {
			jsonError(w, http.StatusNotFound, "rule not found")
			return
		}
		var req alertRuleMutationRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if req.empty() {
			jsonError(w, http.StatusBadRequest, "at least one rule field is required")
			return
		}
		req.apply(existing)
		existing.ID = id
		normalized, err := notify.ValidateAndNormalizeAlertRule(*existing)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing = &normalized
		if err := repo.Update(existing); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, existing)
	}
}

func handleAlertRuleDelete(repo *store.AlertRuleRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			jsonError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := repo.Delete(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				jsonError(w, http.StatusNotFound, "rule not found")
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func handleAlertHistoryList(repo *store.AlertHistoryRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			jsonError(w, http.StatusServiceUnavailable, "notify not initialized")
			return
		}
		limit, offset, err := parseAlertHistoryPagination(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		items, total, err := repo.List(limit, offset)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
	}
}

func parseAlertHistoryPagination(r *http.Request) (int, int, error) {
	query := r.URL.Query()
	for key := range query {
		if key != "limit" && key != "offset" {
			return 0, 0, errors.New("only limit and offset query parameters are supported")
		}
		if len(query[key]) != 1 {
			return 0, 0, errors.New(key + " must be specified once")
		}
	}

	limit, offset := 50, 0
	if query.Has("limit") {
		value := query.Get("limit")
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 200 || value != strconv.Itoa(parsed) {
			return 0, 0, errors.New("limit must be an integer between 1 and 200")
		}
		limit = parsed
	}
	if query.Has("offset") {
		value := query.Get("offset")
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || value != strconv.Itoa(parsed) {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		offset = parsed
	}
	return limit, offset, nil
}

func pathInt64(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}
