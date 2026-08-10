package katago

import (
	"reflect"
	"testing"
)

func TestEngineArgs(t *testing.T) {
	base := []string{"analysis", "-model", "net.bin.gz", "-config", "a.cfg"}

	// Ohne Overrides: nur die Basis.
	got, err := engineArgs("net.bin.gz", "a.cfg", "")

	if err != nil || !reflect.DeepEqual(got, base) {
		t.Errorf("ohne Overrides: %v, %v", got, err)
	}

	// Mehrere Overrides inkl. Whitespace — auch um das "=" herum; die
	// Einträge müssen normalisiert (getrimmt) bei KataGo ankommen.
	got, err = engineArgs("net.bin.gz", "a.cfg",
		" numSearchThreadsPerAnalysisThread = 16 , nnMaxBatchSize=32 ,")

	want := append(append([]string{}, base...),
		"-override-config", "numSearchThreadsPerAnalysisThread=16",
		"-override-config", "nnMaxBatchSize=32")

	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("mit Overrides: %v, %v", got, err)
	}

	// Kaputte Einträge: Fehler statt still verschlucken.
	for _, bad := range []string{"threads", "=16", "threads=", "a=1,b"} {
		if _, err := engineArgs("net.bin.gz", "a.cfg", bad); err == nil {
			t.Errorf("engineArgs(%q): Fehler erwartet", bad)
		}
	}
}
