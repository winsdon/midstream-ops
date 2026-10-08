package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPelicanPersistencePublicationAndIsolation(t *testing.T) {
	r := NewPelicanRepo(newTestStore(t))
	ctx := context.Background()
	b := &PelicanBatch{RequestID: "request-1", Hash: "hash", Request: json.RawMessage(`{}`)}
	results := []*PelicanResult{
		{GroupID: 1, Model: "a", PelicanMetadata: PelicanMetadata{GroupName: "ordinary", Prompt: "snapshot"}},
		{GroupID: 2, Model: "a", PelicanMetadata: PelicanMetadata{GroupName: "exclusive"}},
		{GroupID: 1, Model: "a", PelicanMetadata: PelicanMetadata{GroupName: "ordinary"}},
	}
	if created, err := r.Create(ctx, b, results); err != nil || !created {
		t.Fatalf("create %v %v", created, err)
	}
	dup := &PelicanBatch{RequestID: b.RequestID, Hash: b.Hash, Request: b.Request}
	if created, err := r.Create(ctx, dup, nil); err != nil || created || dup.ID != b.ID {
		t.Fatalf("idempotency %v %v", created, err)
	}
	dup.Hash = "different"
	if _, err := r.Create(ctx, dup, nil); !errors.Is(err, ErrPelicanConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	if err := r.Mutate(ctx, []int64{results[0].ID}, "publish"); !errors.Is(err, ErrPelicanConflict) {
		t.Fatal("published queued work")
	}
	for i, v := range results {
		at := time.Now().UTC().Add(time.Duration(i) * time.Second)
		v.StartedAt = &at
		v.Status = "completed"
		v.HasDocument = true
		v.Output = &PelicanOutput{Document: "<svg></svg>", Kind: "svg", Text: "original"}
		if err := r.Update(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Mutate(ctx, []int64{results[0].ID, results[1].ID, results[2].ID}, "publish"); err != nil {
		t.Fatal(err)
	}
	list, total, err := r.List(ctx, PelicanFilter{Public: true, VisibleGroups: []int64{1}, Latest: true})
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != results[2].ID || list[0].Output != nil {
		t.Fatalf("latest: %+v %d %v", list, total, err)
	}
	if err := r.Mutate(ctx, []int64{results[2].ID}, "unpublish"); err != nil {
		t.Fatal(err)
	}
	list, total, err = r.List(ctx, PelicanFilter{Public: true, VisibleGroups: []int64{1}, Latest: true})
	if err != nil || total != 1 || list[0].ID != results[0].ID {
		t.Fatal("unpublish did not restore older result")
	}
	_, total, err = r.List(ctx, PelicanFilter{Public: true, VisibleGroups: []int64{}})
	if err != nil || total != 0 {
		t.Fatal("empty visibility exposed records")
	}
	if err := r.Mutate(ctx, []int64{results[2].ID, results[0].ID}, "delete"); !errors.Is(err, ErrPelicanConflict) {
		t.Fatal("deleted published result")
	}
	if _, err := r.Get(ctx, results[2].ID); err != nil {
		t.Fatal("batch delete was not atomic")
	}
	got, err := r.Get(ctx, results[0].ID)
	if err != nil || got.Prompt != "snapshot" || got.Output.Document != "<svg></svg>" {
		t.Fatal("lost historical snapshot")
	}
	if err := r.Mutate(ctx, []int64{results[2].ID}, "delete"); err != nil {
		t.Fatal(err)
	}
}
func TestPelicanCancelAndRestartCannotBeOverwritten(t *testing.T) {
	r := NewPelicanRepo(newTestStore(t))
	ctx := context.Background()
	for _, action := range []string{"cancel", "restart"} {
		b := &PelicanBatch{RequestID: action, Hash: action, Request: json.RawMessage(`{}`)}
		v := &PelicanResult{GroupID: 1, Model: "a"}
		if _, err := r.Create(ctx, b, []*PelicanResult{v}); err != nil {
			t.Fatal(err)
		}
		want := "cancelled"
		if action == "cancel" {
			if err := r.Cancel(ctx, b.ID); err != nil {
				t.Fatal(err)
			}
		} else {
			want = "interrupted"
			if err := r.Recover(ctx); err != nil {
				t.Fatal(err)
			}
		}
		v.Status = "completed"
		v.Output = &PelicanOutput{Document: "<svg></svg>"}
		_ = r.Update(ctx, v)
		got, err := r.Get(ctx, v.ID)
		if err != nil || got.Status != want {
			t.Fatalf("resurrected %s: %+v %v", action, got, err)
		}
	}
}
