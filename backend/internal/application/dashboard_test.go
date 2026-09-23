package application_test

import (
	"context"
	"testing"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

func TestDashboardAggregatesRepositoryCounts(t *testing.T) {
	media := &dashboardMediaRepository{total: 42}
	subscriptions := &dashboardSubscriptionRepository{total: 7}
	downloads := &dashboardDownloadRepository{total: 5}
	integrations := healthyIntegrationCounter(3)
	service := application.NewDashboardService(media, subscriptions, downloads, integrations)

	got, err := service.Get(context.Background())
	if err != nil {
		t.Fatalf("get dashboard: %v", err)
	}
	want := domain.Dashboard{ActiveSubscriptions: 7, CompletedDownloads: 5, MediaCount: 42, HealthyIntegrations: 3}
	if got != want {
		t.Fatalf("dashboard = %+v, want %+v", got, want)
	}
	if subscriptions.query.Status != domain.SubscriptionStatusActive {
		t.Fatalf("subscription status filter = %q, want active", subscriptions.query.Status)
	}
	if downloads.query.Status != domain.DownloadStatusCompleted {
		t.Fatalf("download status filter = %q, want completed", downloads.query.Status)
	}
}

type dashboardMediaRepository struct{ total int }

func (r *dashboardMediaRepository) List(context.Context, ports.MediaListQuery) (domain.MediaPage, error) {
	return domain.MediaPage{Items: []domain.Media{}, Total: r.total}, nil
}
func (*dashboardMediaRepository) Get(context.Context, string) (domain.Media, error) {
	return domain.Media{}, ports.ErrMediaNotFound
}

type dashboardSubscriptionRepository struct {
	total int
	query ports.SubscriptionListQuery
}

func (*dashboardSubscriptionRepository) Create(context.Context, ports.CreateSubscription) (domain.Subscription, bool, error) {
	panic("not used")
}
func (*dashboardSubscriptionRepository) Cancel(context.Context, string) (domain.Subscription, bool, error) {
	panic("not used")
}
func (r *dashboardSubscriptionRepository) List(_ context.Context, query ports.SubscriptionListQuery) (domain.SubscriptionPage, error) {
	r.query = query
	return domain.SubscriptionPage{Items: []domain.Subscription{}, Total: r.total}, nil
}

type dashboardDownloadRepository struct {
	total int
	query ports.DownloadListQuery
}

func (r *dashboardDownloadRepository) List(_ context.Context, query ports.DownloadListQuery) (domain.DownloadPage, error) {
	r.query = query
	return domain.DownloadPage{Items: []domain.DownloadTask{}, Total: r.total}, nil
}

type healthyIntegrationCounter int

func (c healthyIntegrationCounter) CountHealthy(context.Context) (int, error) { return int(c), nil }
