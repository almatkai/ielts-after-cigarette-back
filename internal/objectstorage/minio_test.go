package objectstorage

import "testing"

func TestCleanObjectKey(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"", ".", "..", "../secret", "folder/../../secret"} {
		if _, err := cleanObjectKey(key); err == nil {
			t.Errorf("cleanObjectKey(%q) accepted an unsafe key", key)
		}
	}
	got, err := cleanObjectKey(`/listening\example.mp3`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "listening/example.mp3" {
		t.Fatalf("got %q", got)
	}
}
