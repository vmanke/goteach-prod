package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// clock stellt die Zeit, damit die Tests keine Sperren aussitzen müssen.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func testLimiter(cfg Config) (*Limiter, *clock) {
	c := &clock{t: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)}
	l := New(cfg)
	l.now = c.now

	return l, c
}

func strictForTest() Config {
	return Config{
		Burst:       3,
		Refill:      time.Minute,
		BasePenalty: time.Minute,
		MaxPenalty:  time.Hour,
		Forgive:     15 * time.Minute,
		MaxClients:  16,
	}
}

func TestStossWirdDurchgelassenDannGesperrt(t *testing.T) {
	l, _ := testLimiter(strictForTest())

	for i := 0; i < 3; i++ {
		if d := l.Allow("a"); !d.Allowed {
			t.Fatalf("Anfrage %d hätte durchgehen müssen", i+1)
		}
	}

	d := l.Allow("a")

	if d.Allowed {
		t.Fatal("vierte Anfrage hätte abgewiesen werden müssen")
	}

	if d.RetryAfter != time.Minute {
		t.Fatalf("RetryAfter %s, erwartet 1m", d.RetryAfter)
	}

	if d.Strikes != 1 {
		t.Fatalf("Strikes %d, erwartet 1", d.Strikes)
	}
}

func TestStrafeVerdoppeltSichJeVerletzungsphase(t *testing.T) {
	l, c := testLimiter(strictForTest())

	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute}

	for phase, expected := range want {
		// Vorrat auffüllen und wieder leeren, damit eine neue Phase entsteht.
		c.add(10 * time.Minute)

		for i := 0; i < 3; i++ {
			if d := l.Allow("a"); !d.Allowed {
				t.Fatalf("Phase %d: Anfrage %d abgewiesen", phase, i+1)
			}
		}

		d := l.Allow("a")

		if d.Allowed {
			t.Fatalf("Phase %d: Anfrage hätte sperren müssen", phase)
		}

		if d.RetryAfter != expected {
			t.Fatalf("Phase %d: Sperre %s, erwartet %s", phase, d.RetryAfter, expected)
		}
	}
}

func TestSperreWaechstNichtDurchWeitereVersuche(t *testing.T) {
	// Sonst käme ein Client mit automatischer Wiederholung nie wieder
	// heraus — die Eskalation gilt je Phase, nicht je Anfrage.
	l, c := testLimiter(strictForTest())

	for i := 0; i < 4; i++ {
		l.Allow("a")
	}

	for i := 0; i < 50; i++ {
		c.add(time.Second)

		if d := l.Allow("a"); d.Strikes != 1 {
			t.Fatalf("nach %d Versuchen Strikes %d, erwartet 1", i+1, d.Strikes)
		}
	}
}

func TestSperreLaeuftAusUndDannGehtEsWeiter(t *testing.T) {
	l, c := testLimiter(strictForTest())

	for i := 0; i < 4; i++ {
		l.Allow("a")
	}

	c.add(time.Minute + time.Second)

	if d := l.Allow("a"); !d.Allowed {
		t.Fatalf("nach Ablauf der Sperre abgewiesen (RetryAfter %s)", d.RetryAfter)
	}
}

func TestWohlverhaltenSetztDieEskalationZurueck(t *testing.T) {
	l, c := testLimiter(strictForTest())

	for i := 0; i < 4; i++ {
		l.Allow("a")
	}

	// Sperre abgelaufen und danach lange nichts angestellt.
	c.add(time.Minute + 16*time.Minute)

	for i := 0; i < 3; i++ {
		if d := l.Allow("a"); !d.Allowed {
			t.Fatalf("Anfrage %d abgewiesen", i+1)
		}
	}

	d := l.Allow("a")

	if d.Strikes != 1 {
		t.Fatalf("Strikes %d, erwartet 1 (Zurücksetzung)", d.Strikes)
	}

	if d.RetryAfter != time.Minute {
		t.Fatalf("Sperre %s, erwartet wieder die Grundstrafe", d.RetryAfter)
	}
}

func TestVorratFuelltSichNach(t *testing.T) {
	l, c := testLimiter(strictForTest())

	for i := 0; i < 3; i++ {
		l.Allow("a")
	}

	c.add(2 * time.Minute)

	// Zwei Minuten bei einer Auffüllung je Minute ergeben zwei Anfragen.
	for i := 0; i < 2; i++ {
		if d := l.Allow("a"); !d.Allowed {
			t.Fatalf("Anfrage %d nach Auffüllen abgewiesen", i+1)
		}
	}

	if d := l.Allow("a"); d.Allowed {
		t.Fatal("dritte Anfrage hätte abgewiesen werden müssen")
	}
}

func TestStrafeIstGedeckeltUndBleibtPositiv(t *testing.T) {
	cfg := strictForTest()
	l, _ := testLimiter(cfg)

	// Auch bei absurd vielen Stufen darf nichts überlaufen.
	for strikes := 1; strikes < 200; strikes++ {
		penalty := l.penalty(strikes)

		if penalty <= 0 {
			t.Fatalf("Stufe %d ergab %s", strikes, penalty)
		}

		if penalty > cfg.MaxPenalty {
			t.Fatalf("Stufe %d ergab %s über der Obergrenze %s",
				strikes, penalty, cfg.MaxPenalty)
		}
	}

	if l.penalty(99) != cfg.MaxPenalty {
		t.Fatalf("hohe Stufe erreicht die Obergrenze nicht: %s", l.penalty(99))
	}
}

func TestClientsBleibenBegrenzt(t *testing.T) {
	// Die Tabelle darf nicht selbst zum Angriffsziel werden.
	cfg := strictForTest()
	cfg.MaxClients = 8
	l, c := testLimiter(cfg)

	for i := 0; i < 500; i++ {
		c.add(time.Second)
		l.Allow(string(rune('a'+i%200)) + string(rune('0'+i/200)))
	}

	if l.Len() > cfg.MaxClients {
		t.Fatalf("%d Clients gespeichert, Obergrenze %d", l.Len(), cfg.MaxClients)
	}
}

func TestVorstrafeUeberlebtVerdraengungsdruck(t *testing.T) {
	// Sonst wäre das Fluten der Tabelle mit fremden Adressen der bequemste
	// Weg, die eigene Eskalationsstufe zurückzusetzen. Erhalten bleiben muss
	// die *Stufe* — die Sperre selbst läuft ohnehin ab.
	cfg := strictForTest()
	cfg.MaxClients = 4
	l, c := testLimiter(cfg)

	for i := 0; i < 4; i++ {
		l.Allow("täter")
	}

	// Viele fremde Clients, die um die vier Plätze konkurrieren.
	for i := 0; i < 100; i++ {
		c.add(time.Second)
		l.Allow(string(rune('A' + i%90)))
	}

	// Der Täter leert seinen Vorrat erneut: Die Sperre muss jetzt bei zwei
	// Minuten stehen, nicht wieder bei einer.
	for i := 0; i < 10; i++ {
		if d := l.Allow("täter"); !d.Allowed {
			d = l.Allow("täter")

			if d.Strikes != 2 {
				t.Fatalf("Strikes %d, erwartet 2 — Vorstrafe verdrängt", d.Strikes)
			}

			if d.RetryAfter != 2*time.Minute {
				t.Fatalf("Sperre %s, erwartet 2m", d.RetryAfter)
			}

			return
		}
	}

	t.Fatal("der Täter wurde nie wieder gesperrt")
}

func TestKeyNimmtDiePeerAdresse(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:5555"

	if got := Key(r, false); got != "203.0.113.7" {
		t.Fatalf("Key %q", got)
	}
}

func TestKeyIgnoriertForwardedOhneVertrauen(t *testing.T) {
	// Ohne Proxy ist der Header frei erfunden; ihm zu glauben hieße, die
	// Begrenzung mit einer Zeile Header abzuschalten.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := Key(r, false); got != "203.0.113.7" {
		t.Fatalf("Key %q — Header hätte ignoriert werden müssen", got)
	}
}

func TestKeyNimmtDenLetztenForwardedEintrag(t *testing.T) {
	// Die vorderen Einträge stammen vom Client, den letzten schreibt der
	// eigene Proxy.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.7")

	if got := Key(r, true); got != "203.0.113.7" {
		t.Fatalf("Key %q", got)
	}
}

func TestKeyFasstIPv6AufSlash64Zusammen(t *testing.T) {
	first := httptest.NewRequest(http.MethodGet, "/", nil)
	first.RemoteAddr = "[2001:db8:1:2::1]:443"

	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "[2001:db8:1:2::ffff]:443"

	if Key(first, false) != Key(second, false) {
		t.Fatalf("%q und %q sollten dasselbe /64 sein",
			Key(first, false), Key(second, false))
	}

	other := httptest.NewRequest(http.MethodGet, "/", nil)
	other.RemoteAddr = "[2001:db8:1:3::1]:443"

	if Key(first, false) == Key(other, false) {
		t.Fatal("verschiedene /64 dürfen nicht zusammenfallen")
	}
}

func TestClientsStoerenSichNicht(t *testing.T) {
	l, _ := testLimiter(strictForTest())

	for i := 0; i < 4; i++ {
		l.Allow("a")
	}

	if d := l.Allow("b"); !d.Allowed {
		t.Fatal("die Sperre von a darf b nicht treffen")
	}
}
