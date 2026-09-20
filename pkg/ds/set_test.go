package ds

import "testing"

func TestSetFrom(t *testing.T) {
	s := SetFrom("a", "b", "a")
	if s.Size() != 2 {
		t.Fatalf("expected size 2, got %d", s.Size())
	}
	if !s.Contains("a") || !s.Contains("b") {
		t.Fatal("expected a and b in set")
	}
}

func TestAtomicSetLoadAndStoreAndDelete(t *testing.T) {
	set := NewSyncedSet[string]()

	if loaded := set.Add("task-1"); loaded {
		t.Fatal("first Add should report missing item")
	}

	if loaded := set.Add("task-1"); !loaded {
		t.Fatal("second Add should report existing item")
	}

	if !set.Contains("task-1") {
		t.Fatal("item should exist after Add")
	}

	if deleted := set.Remove("task-1"); !deleted {
		t.Fatal("Remove should remove existing item")
	}

	if deleted := set.Remove("task-1"); deleted {
		t.Fatal("Remove should report missing item after removal")
	}
}
