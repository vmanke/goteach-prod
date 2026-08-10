// Package ratelimit begrenzt Anfragen je Client und bestraft Wiederholungs-
// täter mit exponentiell wachsenden Sperren.
//
// Der Anlass ist kein theoretischer: Eine Analyse bindet die Engine für
// Minuten (auf einer 4-Kern-CPU rund sieben Minuten für zehn Züge bei 30
// Visits). Ein einzelner Angreifer legt den Dienst also mit einer Handvoll
// Anfragen lahm — hier kostet eine Anfrage nicht Millisekunden, sondern
// Rechenzeit in der Größenordnung eines Kaffees.
//
// Zwei Mechanismen, weil einer nicht reicht:
//
//   - Ein Token-Bucket je Client erlaubt kurze Stöße und drosselt danach.
//   - Wer den Bucket leert, wird gesperrt; jede weitere Verletzungsphase
//     verdoppelt die Sperrdauer bis zu einer Obergrenze.
//
// Nur Standardbibliothek, wie das ganze Modul.
package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Config beschreibt eine Begrenzung.
type Config struct {
	// Burst ist die Zahl der Anfragen, die ein Client sofort stellen darf.
	Burst float64

	// Refill ist die Rate, mit der sich der Vorrat wieder füllt.
	Refill time.Duration

	// BasePenalty ist die erste Sperre nach einer Verletzung.
	BasePenalty time.Duration

	// MaxPenalty deckelt die Verdopplung.
	MaxPenalty time.Duration

	// Forgive setzt die Eskalationsstufe zurück, wenn ein Client so lange
	// nichts mehr angestellt hat. Ohne das bliebe ein einmaliger Ausrutscher
	// für immer teuer — und ein Client hinter NAT wäre dauerhaft bestraft,
	// weil er sich die Adresse mit jemandem teilt.
	Forgive time.Duration

	// MaxClients begrenzt die Zahl beobachteter Clients. Die Tabelle ist
	// sonst selbst ein Angriffsziel: Wer mit vielen Quelladressen anfragt,
	// füllte sonst den Speicher. Bei Überlauf fliegt der am längsten
	// ungesehene Eintrag.
	MaxClients int
}

// Strict passt zu teuren Endpunkten (/analyze): drei Anfragen sofort, danach
// eine je Minute.
func Strict() Config {
	return Config{
		Burst:       3,
		Refill:      time.Minute,
		BasePenalty: time.Minute,
		MaxPenalty:  time.Hour,
		Forgive:     15 * time.Minute,
		MaxClients:  4096,
	}
}

// Lenient passt zu billigen Endpunkten (Startseite, Assets, Healthcheck).
func Lenient() Config {
	return Config{
		Burst:       60,
		Refill:      time.Second,
		BasePenalty: 5 * time.Second,
		MaxPenalty:  5 * time.Minute,
		Forgive:     5 * time.Minute,
		MaxClients:  8192,
	}
}

type client struct {
	tokens    float64
	seen      time.Time
	strikes   int
	blockedTo time.Time
}

// Limiter ist nebenläufig benutzbar.
type Limiter struct {
	cfg Config
	now func() time.Time

	mu      sync.Mutex
	clients map[string]*client
}

// New liefert einen Limiter.
func New(cfg Config) *Limiter {
	if cfg.MaxClients <= 0 {
		cfg.MaxClients = 4096
	}

	if cfg.Refill <= 0 {
		cfg.Refill = time.Second
	}

	return &Limiter{
		cfg:     cfg,
		now:     time.Now,
		clients: make(map[string]*client),
	}
}

// Decision ist das Ergebnis einer Prüfung.
type Decision struct {
	// Allowed meldet, ob die Anfrage durchgelassen wird.
	Allowed bool

	// RetryAfter ist die Wartezeit bis zum nächsten Versuch; nur bei
	// Allowed == false gesetzt.
	RetryAfter time.Duration

	// Strikes ist die erreichte Eskalationsstufe (für Logs).
	Strikes int
}

// Allow prüft einen Client und bucht bei Erfolg ein Token ab.
func (l *Limiter) Allow(key string) Decision {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.clients[key]

	if entry == nil {
		l.evictIfFull(now)

		entry = &client{tokens: l.cfg.Burst, seen: now}
		l.clients[key] = entry
	}

	// Nachfüllen nach verstrichener Zeit.
	elapsed := now.Sub(entry.seen)

	if elapsed > 0 {
		entry.tokens += elapsed.Seconds() / l.cfg.Refill.Seconds()

		if entry.tokens > l.cfg.Burst {
			entry.tokens = l.cfg.Burst
		}
	}

	entry.seen = now

	// Wer gesperrt ist, wird abgewiesen — aber die Sperre wächst dabei
	// *nicht* weiter. Sonst käme ein Client mit einer automatischen
	// Wiederholung nie wieder heraus, und schon ein hängender Browser-Tab
	// führte zur Höchststrafe. Eskaliert wird je Verletzungsphase, nicht je
	// abgewiesener Anfrage.
	if now.Before(entry.blockedTo) {
		return Decision{RetryAfter: entry.blockedTo.Sub(now), Strikes: entry.strikes}
	}

	// Straffrei nach ausreichend langem Wohlverhalten. Gemessen ab dem Ende
	// der letzten Sperre.
	if entry.strikes > 0 && l.cfg.Forgive > 0 &&
		now.Sub(entry.blockedTo) >= l.cfg.Forgive {
		entry.strikes = 0
	}

	if entry.tokens >= 1 {
		entry.tokens--

		return Decision{Allowed: true, Strikes: entry.strikes}
	}

	// Verletzung: neue Phase, Sperre verdoppeln.
	entry.strikes++
	penalty := l.penalty(entry.strikes)
	entry.blockedTo = now.Add(penalty)

	return Decision{RetryAfter: penalty, Strikes: entry.strikes}
}

// penalty verdoppelt je Stufe bis MaxPenalty.
func (l *Limiter) penalty(strikes int) time.Duration {
	penalty := l.cfg.BasePenalty

	for i := 1; i < strikes; i++ {
		penalty *= 2

		// Vor der Obergrenze abbrechen, damit die Verdopplung bei vielen
		// Stufen nicht über den int64-Bereich läuft und negativ wird.
		if penalty >= l.cfg.MaxPenalty {
			return l.cfg.MaxPenalty
		}
	}

	if l.cfg.MaxPenalty > 0 && penalty > l.cfg.MaxPenalty {
		return l.cfg.MaxPenalty
	}

	return penalty
}

// evictIfFull schafft Platz, bevor ein neuer Eintrag entsteht. Aufrufer hält
// den Lock.
func (l *Limiter) evictIfFull(now time.Time) {
	if len(l.clients) < l.cfg.MaxClients {
		return
	}

	// Erst alles wegräumen, was ohnehin abgelaufen ist.
	for key, entry := range l.clients {
		if now.After(entry.blockedTo) && now.Sub(entry.seen) > l.cfg.Forgive {
			delete(l.clients, key)
		}
	}

	if len(l.clients) < l.cfg.MaxClients {
		return
	}

	// Sonst den am längsten ungesehenen Eintrag opfern — aber Einträge mit
	// Vorstrafen zuletzt. Sie sind genau die, auf die es ankommt: Wer sie
	// verdrängen kann, setzt seine Eskalationsstufe zurück und umgeht die
	// Bestrafung, indem er die Tabelle mit fremden Adressen flutet.
	oldestKey, found := l.leastRecent(now, false)

	if !found {
		// Alles vorbestraft: Es muss trotzdem Platz entstehen, sonst wächst
		// die Tabelle unbegrenzt. Unter einem verteilten Angriff geht
		// Strafhistorie verloren — das ist der Preis einer festen Obergrenze
		// und die bewusste Wahl gegen unbegrenzten Speicher.
		oldestKey, found = l.leastRecent(now, true)
	}

	if found {
		delete(l.clients, oldestKey)
	}
}

// leastRecent liefert den am längsten ungesehenen Eintrag. Ist includeGuarded
// falsch, bleiben gesperrte und vorbestrafte Einträge außen vor.
func (l *Limiter) leastRecent(now time.Time, includeGuarded bool) (string, bool) {
	var (
		oldestKey string
		oldest    time.Time
		found     bool
	)

	for key, entry := range l.clients {
		if !includeGuarded && (now.Before(entry.blockedTo) || entry.strikes > 0) {
			continue
		}

		if !found || entry.seen.Before(oldest) {
			oldestKey, oldest, found = key, entry.seen, true
		}
	}

	return oldestKey, found
}

// Len liefert die Zahl beobachteter Clients (für Tests und Diagnose).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.clients)
}

// Key bestimmt den Client einer Anfrage.
//
// Standardmäßig zählt allein die Peer-Adresse. X-Forwarded-For wird nur
// ausgewertet, wenn trustProxy gesetzt ist — und dann der *letzte* Eintrag,
// denn den hat der eigene Proxy geschrieben. Die vorderen Einträge stammen
// vom Client und sind frei erfindbar; wer ihnen glaubt, baut eine Umgehung
// statt einer Begrenzung.
//
// IPv6 wird auf das /64 zusammengefasst: Ein einzelner Anschluss bekommt dort
// üblicherweise ein ganzes Präfix, und eine Begrenzung je Einzeladresse wäre
// mit einem Zeilenumbruch zu umgehen.
func Key(r *http.Request, trustProxy bool) string {
	host := r.RemoteAddr

	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}

	if trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			last := strings.TrimSpace(parts[len(parts)-1])

			if last != "" {
				host = last
			}
		}
	}

	ip := net.ParseIP(host)

	if ip == nil {
		return host
	}

	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}

	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
