// Hydratisiert die serverseitig gerenderte Seite: liest den eingebetteten
// Zustand (#goteach-state), verdrahtet Upload-Formular, Drag & Drop,
// Ergebnis-Rendering und den clientseitigen JSON-Download. Ohne JavaScript
// bleibt das Formular als klassischer Multipart-POST auf /analyze nutzbar.
(function () {
  'use strict';

  var stateEl = document.getElementById('goteach-state');
  var state = {};

  try {
    state = stateEl ? JSON.parse(stateEl.textContent) : {};
  } catch (err) {
    state = {};
  }

  // Hydration-Marker: blendet .js-only ein und .no-js-only aus (style.css).
  document.documentElement.dataset.js = 'hydrated';

  // Theme-Umschalter: explizite Wahl überstimmt prefers-color-scheme und
  // wird in localStorage gemerkt (Head-Skript wendet sie vor dem Paint an).
  var themeToggle = document.getElementById('theme-toggle');

  if (themeToggle) {
    themeToggle.addEventListener('click', function () {
      var root = document.documentElement;
      var systemDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
      var current = root.dataset.theme || (systemDark ? 'dark' : 'light');
      var next = current === 'dark' ? 'light' : 'dark';

      root.dataset.theme = next;

      try {
        localStorage.setItem('goteach-theme', next);
      } catch (err) { /* privater Modus o. Ä.: Wahl gilt nur für diese Seite */ }
    });
  }

  var form = document.getElementById('analyze-form');

  if (!form) {
    return;
  }

  var fileInput = form.querySelector('input[type="file"][name="sgf"]');
  var textInput = form.querySelector('textarea[name="sgf"]');
  var dropzone = document.getElementById('dropzone');
  var analyzeBtn = document.getElementById('analyze-btn');
  var downloadBtn = document.getElementById('download-btn');
  var statusEl = document.getElementById('status');
  var results = document.getElementById('results');
  var resultsSummary = document.getElementById('results-summary');
  var resultsList = document.getElementById('results-list');
  var lastReport = null;

  function setStatus(text, isError) {
    statusEl.textContent = text || '';
    statusEl.classList.toggle('error', Boolean(isError));
  }

  function busy(on) {
    analyzeBtn.disabled = on;
    analyzeBtn.textContent = on ? 'Analysiere …' : 'Analysieren';
  }

  // Drag & Drop auf die Dropzone legt die Datei in das File-Input.
  if (dropzone && fileInput) {
    ['dragenter', 'dragover'].forEach(function (name) {
      dropzone.addEventListener(name, function (ev) {
        ev.preventDefault();
        dropzone.classList.add('dragging');
      });
    });

    ['dragleave', 'drop'].forEach(function (name) {
      dropzone.addEventListener(name, function (ev) {
        ev.preventDefault();
        dropzone.classList.remove('dragging');
      });
    });

    dropzone.addEventListener('drop', function (ev) {
      if (ev.dataTransfer && ev.dataTransfer.files.length > 0) {
        fileInput.files = ev.dataTransfer.files;
        setStatus('Datei übernommen: ' + ev.dataTransfer.files[0].name, false);
      }
    });
  }

  function readSGF() {
    if (fileInput && fileInput.files && fileInput.files.length > 0) {
      return fileInput.files[0].text();
    }

    var text = textInput ? textInput.value.trim() : '';

    return Promise.resolve(text);
  }

  function queryFromOptions() {
    var params = new URLSearchParams();

    ['visits', 'tau', 'from', 'to', 'rules', 'komi'].forEach(function (name) {
      var field = form.elements.namedItem(name);
      var value = field && field.value ? String(field.value).trim() : '';

      if (value !== '') {
        params.set(name, value);
      }
    });

    return params;
  }

  function categoryClass(category) {
    var known = {
      'ausgezeichnet': 'cat-excellent',
      'gut': 'cat-good',
      'Ungenauigkeit': 'cat-inaccuracy',
      'Fehler': 'cat-mistake',
      'grober Fehler': 'cat-blunder'
    };

    return known[category] || 'cat-other';
  }

  // Rendert die Reports ausschließlich über textContent (kein innerHTML
  // mit Nutzdaten) — SGF-Inhalte bleiben so immer inert.
  function render(payload) {
    resultsList.textContent = '';

    var summary = payload.moves + ' Züge, Brett ' + payload.size + '×' +
      payload.size + ', Komi ' + payload.komi;

    if (payload.rules) {
      summary += ', Regeln ' + payload.rules;
    }

    if (payload.synthetic) {
      summary += ' — Achtung: SYNTHETISCHE Werte (Mock, keine echte Engine).';
    }

    resultsSummary.textContent = summary;

    (payload.reports || []).forEach(function (report) {
      var article = document.createElement('article');
      article.className = 'report';

      var head = document.createElement('header');
      var badge = document.createElement('span');
      badge.className = 'badge ' + categoryClass(report.category);
      badge.textContent = report.category;

      var title = document.createElement('strong');
      title.textContent = 'Zug ' + report.number + ' — ' + report.player +
        ' ' + report.coord;

      head.appendChild(title);
      head.appendChild(badge);
      article.appendChild(head);

      var text = document.createElement('pre');
      text.className = 'report-text';
      text.textContent = report.textLLM || report.text || '';
      article.appendChild(text);

      resultsList.appendChild(article);
    });

    results.hidden = false;
    downloadBtn.hidden = false;
    results.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }

  form.addEventListener('submit', function (ev) {
    ev.preventDefault();
    setStatus('', false);

    readSGF().then(function (sgf) {
      if (!sgf) {
        setStatus('Bitte eine SGF-Datei wählen oder SGF-Text einfügen.', true);

        return null;
      }

      if (state.maxSGFBytes && sgf.length > state.maxSGFBytes) {
        setStatus('SGF größer als ' + state.maxSGFBytes + ' Bytes.', true);

        return null;
      }

      busy(true);

      return fetch('/analyze?' + queryFromOptions().toString(), {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-go-sgf' },
        body: sgf
      }).then(function (res) {
        return res.json().then(function (payload) {
          if (!res.ok) {
            throw new Error(payload.error || ('HTTP ' + res.status));
          }

          lastReport = payload;
          render(payload);
          setStatus('Analyse abgeschlossen.', false);
        });
      });
    }).catch(function (err) {
      setStatus('Fehler: ' + err.message, true);
    }).then(function () {
      busy(false);
    });
  });

  // Clientseitiger Download des zuletzt geladenen Reports als JSON-Datei.
  downloadBtn.addEventListener('click', function () {
    if (!lastReport) {
      return;
    }

    var blob = new Blob([JSON.stringify(lastReport, null, 2)],
      { type: 'application/json' });
    var url = URL.createObjectURL(blob);
    var a = document.createElement('a');
    a.href = url;
    a.download = 'goteach-report.json';
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  });
})();
