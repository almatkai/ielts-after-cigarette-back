package fullmock

import (
	"github.com/google/uuid"
	"testing"
)

func TestSelectionPool(t *testing.T) {
	unseen := candidate{skill: "listening", materialID: uuid.New(), previous: true}
	done := candidate{skill: "listening", materialID: uuid.New(), completed: true}
	last := candidate{skill: "listening", materialID: uuid.New(), completed: true, previous: true}
	for _, tc := range []struct {
		name  string
		items []candidate
		want  []candidate
	}{
		{"unseen wins even if previously started", []candidate{done, last, unseen}, []candidate{unseen}},
		{"repeat avoids last draw", []candidate{done, last}, []candidate{done}},
		{"one material can repeat", []candidate{last}, []candidate{last}},
		{"other skill is ignored", []candidate{{skill: "reading"}, done}, []candidate{done}},
		{"empty bank", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := selectionPool(tc.items, "listening")
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestBankStatuses(t *testing.T) {
	items := []candidate{{skill: "listening", completed: true}, {skill: "reading"}, {skill: "writing", completed: true}, {skill: "speaking"}}
	banks, ready := bankStatuses(items)
	if !ready || len(banks) != 4 || !banks[0].IsExhausted || banks[0].Remaining != 0 || banks[1].IsExhausted || banks[1].Remaining != 1 {
		t.Fatalf("%+v ready=%v", banks, ready)
	}
	banks, ready = bankStatuses(nil)
	if ready || banks[0].IsExhausted {
		t.Fatal("empty bank is unavailable, not exhausted")
	}
}
