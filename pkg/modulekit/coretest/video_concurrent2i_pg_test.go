package coretest

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/calendar"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Independent service instances share only Postgres, as separate core processes do.
func TestCoreUpdate2IVideoConcurrentMerge(t *testing.T) {
	c := New(t)
	app := c.app
	second, err := calendar.New(calendar.Deps{Store: calendar.NewPostgres(app.Pool), Policy: app.Practice.Policy, Matters: calendar.PracticeMatters{Svc: app.Practice}, Users: app.Users, Settings: app.Settings, Audit: app.Audit})
	if err != nil {
		t.Fatal(err)
	}
	p := domain.Principal{UserID: domain.ID(c.Attorney.ID), Roles: []domain.Role{domain.RoleAttorney}, Source: domain.SourceUI}
	ctx := context.Background()
	host := domain.ID(c.Attorney.ID)
	event, err := app.Calendar.CreateEvent(ctx, p, calendar.EventInput{Title: "Concurrent video", Kind: calendar.KindClientMeeting, StartsAt: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 24, 11, 0, 0, 0, time.UTC), Video: &domain.CalendarVideo{Requested: true, HostUserID: &host}})
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 8; round++ {
		provider, meeting := fmt.Sprintf("provider-%d", round), fmt.Sprintf("meeting-%d", round)
		providerValue, meetingValue := &provider, &meeting
		patches := []*calendar.VideoPatch{{Provider: &providerValue}, {ProviderMeetingID: &meetingValue}}
		services := []*calendar.Service{app.Calendar, second}
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for i := range services {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, err := services[i].UpdateEvent(ctx, p, event.ID, calendar.EventPatch{Video: &patches[i]})
				results <- err
			}(i)
		}
		close(start)
		wg.Wait()
		for range services {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		got, err := app.Calendar.GetEvent(ctx, p, event.ID)
		if err != nil || got.Video == nil || got.Video.Provider == nil || *got.Video.Provider != provider || got.Video.ProviderMeetingID == nil || *got.Video.ProviderMeetingID != meeting || !got.Video.Requested || got.Video.HostUserID == nil || *got.Video.HostUserID != host {
			t.Fatalf("lost concurrent field: %+v %v", got.Video, err)
		}
	}
	// Each committed patch audits only its own field, preserving the other writer's
	// value in both its old and new video snapshots.
	rows, err := app.Pool.Query(`SELECT old_value,new_value FROM core.audit_log WHERE entity='calendar_event' AND entity_id=$1 AND field='video' AND old_value<>'null'`, string(event.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var oldText, newText string
		if err := rows.Scan(&oldText, &newText); err != nil {
			t.Fatal(err)
		}
		var oldVideo, newVideo map[string]any
		if err := json.Unmarshal([]byte(oldText), &oldVideo); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(newText), &newVideo); err != nil {
			t.Fatal(err)
		}
		changed := 0
		for key, value := range oldVideo {
			if !reflect.DeepEqual(value, newVideo[key]) {
				if key != "provider" && key != "provider_meeting_id" {
					t.Fatalf("unrequested audit change %s", key)
				}
				changed++
			}
		}
		if changed != 1 {
			t.Fatalf("audit lost concurrent field: %s -> %s", oldText, newText)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 16 {
		t.Fatalf("audit patches=%d", count)
	}

}
