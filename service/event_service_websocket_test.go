package service

import (
	"context"
	"testing"
	"time"

	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"github.com/IceWhaleTech/CasaOS-MessageBus/model"
	"github.com/IceWhaleTech/CasaOS-MessageBus/repository"
	"gotest.tools/assert"
)

// subscription before Start (goroutine) must survive Start
func TestSubscriptionBeforeStartSurvivesStart(t *testing.T) {
	logger.LogInitConsoleOnly() // Publish logs while the loop is not up yet

	repository, err := repository.NewDatabaseRepositoryInMemory()
	assert.NilError(t, err)
	defer repository.Close()

	typeService := NewEventTypeService(&repository)
	wsService := NewEventServiceWS(typeService)

	_, err = typeService.RegisterEventType(model.EventType{SourceID: "early", Name: "early:event"})
	assert.NilError(t, err)

	channel, err := wsService.Subscribe("early", []string{"early:event"})
	assert.NilError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wsService.Start(&ctx)

	// Publish drops events while the loop isn't receiving → republish until received
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case event := <-channel:
			assert.Equal(t, event.Name, "early:event")
			return
		case <-tick.C:
			wsService.Publish(model.Event{SourceID: "early", Name: "early:event"})
		case <-deadline:
			t.Fatal("a subscription made before Start never received an event")
		}
	}
}

// publish before Start: dropped, no nil-ctx panic
func TestPublishBeforeStartDoesNotPanic(t *testing.T) {
	logger.LogInitConsoleOnly()

	repository, err := repository.NewDatabaseRepositoryInMemory()
	assert.NilError(t, err)
	defer repository.Close()

	wsService := NewEventServiceWS(NewEventTypeService(&repository))
	wsService.Publish(model.Event{SourceID: "early", Name: "early:event"})
}
