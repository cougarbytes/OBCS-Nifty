package runner

import "testing"

func TestShortCoveredPriceRoundTrip(t *testing.T) {
	note := shortCoveredMarker + "12.3400"
	p, ok := shortCoveredPrice(note)
	if !ok || p != 12.34 {
		t.Fatalf("shortCoveredPrice(%q) = %v,%v; want 12.34,true", note, p, ok)
	}
}

func TestShortCoveredPriceHandlesTrailingText(t *testing.T) {
	p, ok := shortCoveredPrice(shortCoveredMarker + "8.0500 (long sell pending)")
	if !ok || p != 8.05 {
		t.Fatalf("got %v,%v; want 8.05,true", p, ok)
	}
}

func TestShortCoveredPriceRejectsUnmarkedNotes(t *testing.T) {
	for _, note := range []string{"", "some other note", shortCoveredMarker + "garbage"} {
		if _, ok := shortCoveredPrice(note); ok {
			t.Errorf("shortCoveredPrice(%q) matched; want no match", note)
		}
	}
}
