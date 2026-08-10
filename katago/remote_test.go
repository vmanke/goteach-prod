package katago

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteAnalyzeGame(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/engine/analyze" || r.Method != http.MethodPost {
				http.NotFound(w, r)

				return
			}

			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Errorf("Authorization = %q, erwartet Bearer tok", got)
			}

			var q remoteQuery

			if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
				t.Errorf("Query unlesbar: %v", err)
			}

			if q.Request.Size != 19 || len(q.Turns) != 2 {
				t.Errorf("Query falsch: %+v", q)
			}

			_ = json.NewEncoder(w).Encode(remoteReply{
				Synthetic: true,
				Results: []*Result{
					{TurnNumber: 0}, {TurnNumber: 1},
				},
			})
		}))
	defer stub.Close()

	rm := NewRemote(stub.URL+"/", "tok")

	results, err := rm.AnalyzeGame(Request{Size: 19, Komi: 7.5}, []int{0, 1})

	if err != nil {
		t.Fatalf("AnalyzeGame: %v", err)
	}

	if len(results) != 2 || results[1].TurnNumber != 1 {
		t.Errorf("Ergebnisse falsch: %+v", results)
	}

	if !rm.Synthetic() {
		t.Error("synthetic-Flag nicht durchgereicht")
	}

	if err := rm.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestRemoteAnalyzeGameHTTPError(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"Engine kaputt"}`))
		}))
	defer stub.Close()

	rm := NewRemote(stub.URL, "tok")

	_, err := rm.AnalyzeGame(Request{Size: 19}, []int{0})

	if !errors.Is(err, ErrRemote) {
		t.Fatalf("err = %v, erwartet ErrRemote", err)
	}
}

func TestRemoteAnalyzeGameResultCountMismatch(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(remoteReply{Results: []*Result{}})
		}))
	defer stub.Close()

	rm := NewRemote(stub.URL, "tok")

	if _, err := rm.AnalyzeGame(Request{Size: 19}, []int{0}); !errors.Is(err, ErrRemote) {
		t.Fatalf("err = %v, erwartet ErrRemote", err)
	}
}

func TestRemoteAnalyzeGameConnectionRefused(t *testing.T) {
	rm := NewRemote("http://127.0.0.1:1", "tok")

	if _, err := rm.AnalyzeGame(Request{Size: 19}, []int{0}); !errors.Is(err, ErrRemote) {
		t.Fatalf("err = %v, erwartet ErrRemote", err)
	}
}
