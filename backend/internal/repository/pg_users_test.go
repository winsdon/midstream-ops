package repository

import (
	"context"
	"testing"
)

func TestBalancesByIDsEmpty(t *testing.T) {
	p := &PG{}
	got, err := p.BalancesByIDs(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("空 ids 应返回空 map，实际 %v", got)
	}
	got, err = p.BalancesByIDs(context.Background(), []int64{})
	if err != nil || len(got) != 0 {
		t.Fatalf("空切片应返回空 map，实际 %v err=%v", got, err)
	}
}
