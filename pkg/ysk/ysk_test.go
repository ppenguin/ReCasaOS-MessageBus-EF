package ysk_test

import (
	"context"
	"testing"
	"time"

	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"github.com/IceWhaleTech/CasaOS-MessageBus/model"
	"github.com/IceWhaleTech/CasaOS-MessageBus/pkg/ysk"
	"github.com/IceWhaleTech/CasaOS-MessageBus/repository"
	"github.com/IceWhaleTech/CasaOS-MessageBus/service"
	"github.com/IceWhaleTech/CasaOS-MessageBus/utils"
	"gotest.tools/assert"
)

var ws *service.EventServiceWS

func setup(t *testing.T) (*service.EventServiceWS, *service.YSKService, func()) {
	repository, err := repository.NewDatabaseRepositoryInMemory()
	assert.NilError(t, err)
	s := service.NewServices(&repository)
	wsService := s.EventServiceWS
	yskService := s.YSKService

	ctx := context.Background()
	go s.Start(&ctx)
	return wsService, yskService, func() {
		repository.Close()
	}
}
func mockPublish(ctx context.Context, sourceID string, eventName string, body map[string]string) {
	if ws != nil {
		ws.Publish(model.Event{
			SourceID:   sourceID,
			Name:       eventName,
			Properties: body,
		})
	}
}

// eventually: republish (idempotent) until cards with ids == want; replaces sleeps (flaky under load).
// counts own ids only: in-memory repo = shared-cache DB, other tests' cards visible
func eventually(t *testing.T, yskService *service.YSKService, ids []string, want int, publish func()) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		publish()
		cards, err := yskService.YskCardList(context.Background())
		assert.NilError(t, err)
		got := 0
		for _, card := range cards {
			for _, id := range ids {
				if card.Id == id {
					got++
				}
			}
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cards %v = %d, want %d", ids, got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestUpdateProgress(t *testing.T) {
	logger.LogInitConsoleOnly()

	wsService, yskService, cleanup := setup(t)
	defer cleanup()
	ws = wsService

	yskService.Start(false)

	eventually(t, yskService, []string{utils.ApplicationInstallProgress.Id}, 1, func() {
		err := ysk.NewYSKCard(context.Background(), utils.ApplicationInstallProgress.WithTaskContent(
			"jellyfin logo",
			"Installing LinuxServer/Jellyfin",
		).WithProgress(
			"Installing LinuxServer/Jellyfin", 25,
		), mockPublish)
		assert.NilError(t, err)

		err = ysk.NewYSKCard(context.Background(), utils.ApplicationInstallProgress.WithProgress(
			"Installing LinuxServer/Jellyfin", 50,
		), mockPublish)
		assert.NilError(t, err)
	})

	eventually(t, yskService, []string{utils.ApplicationInstallProgress.Id}, 0, func() {
		err := ysk.DeleteCard(context.Background(), utils.ApplicationInstallProgress.Id, mockPublish)
		assert.NilError(t, err)
	})
}

func TestLongAndShortNoticeInsert(t *testing.T) {
	logger.LogInitConsoleOnly()

	wsService, yskService, cleanup := setup(t)
	defer cleanup()
	ws = wsService

	yskService.Start(false)

	eventually(t, yskService, []string{utils.ZimaOSDataStationNotice.Id, utils.ApplicationUpdateNotice.Id}, 1, func() {
		err := ysk.NewYSKCard(context.Background(), utils.ZimaOSDataStationNotice, mockPublish)
		assert.NilError(t, err)
		err = ysk.NewYSKCard(context.Background(), utils.ApplicationUpdateNotice, mockPublish)
		assert.NilError(t, err)
	})
}
