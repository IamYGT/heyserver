package db

import (
	"time"
	"github.com/IamYGT/heyserver/internal/models"
)

// NotificationChannelRepository manages notification channel records.
type NotificationChannelRepository struct{}

func (r *NotificationChannelRepository) List() ([]*models.NotificationChannel, error) { return nil, nil }
func (r *NotificationChannelRepository) Get(id int64) (*models.NotificationChannel, error) { return nil, nil }
func (r *NotificationChannelRepository) Create(ch *models.NotificationChannel) error { return nil }
func (r *NotificationChannelRepository) Update(ch *models.NotificationChannel) error { return nil }
func (r *NotificationChannelRepository) Delete(id int64) error { return nil }

// AlertRuleRepository manages alert rule records.
type AlertRuleRepository struct{}

func (r *AlertRuleRepository) List() ([]*models.AlertRule, error) { return nil, nil }
func (r *AlertRuleRepository) Get(id int64) (*models.AlertRule, error) { return nil, nil }
func (r *AlertRuleRepository) Create(rule *models.AlertRule) error { return nil }
func (r *AlertRuleRepository) Update(rule *models.AlertRule) error { return nil }
func (r *AlertRuleRepository) Delete(id int64) error { return nil }

// AlertHistoryRepository manages alert history records.
type AlertHistoryRepository struct{}

func (r *AlertHistoryRepository) List(ruleID int64, limit int) ([]*models.AlertHistory, error) { return nil, nil }
func (r *AlertHistoryRepository) Insert(h *models.AlertHistory) error { return nil }
func (r *AlertHistoryRepository) LastFiredAt(ruleID int64) (*time.Time, error) { return nil, nil }
func (r *AlertHistoryRepository) PruneOlderThan(days int) error { return nil }
