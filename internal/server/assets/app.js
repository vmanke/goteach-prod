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

  function effectiveTheme() {
    var systemDark = window.matchMedia('(prefers-color-scheme: dark)').matches;

    return document.documentElement.dataset.theme ||
      (systemDark ? 'dark' : 'light');
  }

  function updateThemeLabel() {
    if (themeToggle) {
      themeToggle.textContent =
        effectiveTheme() === 'dark' ? '☀ Hell' : '☾ Dunkel';
    }
  }

  if (themeToggle) {
    updateThemeLabel();

    themeToggle.addEventListener('click', function () {
      var next = effectiveTheme() === 'dark' ? 'light' : 'dark';

      document.documentElement.dataset.theme = next;
      updateThemeLabel();

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
  var imageInput = form.querySelector('input[type="file"][name="image"]');
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

    ['visits', 'tau', 'from', 'to', 'rules', 'komi', 'ogs'].forEach(function (name) {
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

    if (payload.strands && payload.strands.length) {
      summary = payload.strands.length + ' Erzählstränge aus ' + summary;
    }

    if (payload.rules) {
      summary += ', Regeln ' + payload.rules;
    }

    if (payload.synthetic) {
      summary += ' — Achtung: SYNTHETISCHE Werte (Mock, keine echte Engine).';
    }

    if (payload.position) {
      // Ein Brettfoto zeigt eine Stellung, keine Partie: statt Zug-Reports
      // gibt es einen Stellungsbericht.
      summary = 'Erkannte Stellung, Brett ' + payload.size + '×' +
        payload.size + ', Komi ' + payload.komi +
        (payload.rules ? ', Regeln ' + payload.rules : '');

      if (payload.synthetic) {
        summary += ' — Achtung: SYNTHETISCHE Werte (Mock, keine echte Engine).';
      }
    }

    resultsSummary.textContent = summary;

    if (payload.position) {
      renderPosition(payload.position);
    }

    (payload.strands || []).forEach(renderStrand);

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

  // Ein Erzählstrang: die Hauptsicht auf eine Partie. Zug-Reports bleiben
  // darunter als Detailebene stehen.
  function renderStrand(strand) {
    var article = document.createElement('article');
    article.className = 'report';

    var head = document.createElement('header');
    var title = document.createElement('strong');
    title.textContent = 'Strang ' + strand.id + ' — ' + strand.area +
      ', Züge ' + strand.fromMove + '–' + strand.toMove;
    head.appendChild(title);

    var badge = document.createElement('span');
    badge.className = 'badge';
    badge.textContent = (strand.moves || []).length + ' Züge';
    head.appendChild(badge);
    article.appendChild(head);

    var text = document.createElement('pre');
    text.className = 'report-text';
    text.textContent = strand.textLLM || strand.text || '';
    article.appendChild(text);

    (strand.couplings || []).forEach(function (coupling) {
      var line = document.createElement('div');

      // Bewusst "hängt zeitlich zusammen": Die Kreuzkorrelation zeigt einen
      // Zusammenhang über die Zeit, keine Ursache.
      line.textContent = coupling.from + ' hängt zeitlich zusammen mit ' +
        coupling.to + ' (r = ' + coupling.correlation.toFixed(2) +
        ', Versatz ' + coupling.lag + ' Züge)';
      article.appendChild(line);
    });

    resultsList.appendChild(article);
  }

  // Stellungsbericht eines erkannten Bretts: Lehrtext plus Kettenliste.
  function renderPosition(position) {
    var article = document.createElement('article');
    article.className = 'report';

    var head = document.createElement('header');
    var title = document.createElement('strong');
    title.textContent = 'Stellung — ' + position.stones + ' Steine';
    head.appendChild(title);
    article.appendChild(head);

    var text = document.createElement('pre');
    text.className = 'report-text';
    text.textContent = position.text || '';
    article.appendChild(text);

    (position.groups || []).forEach(function (group) {
      var line = document.createElement('div');
      var marks = (group.inAtari ? ' [Atari]' : '') +
        (group.uncondAlive ? ' [unbedingt lebendig]' : '');

      line.textContent = group.color + ' ' + group.rep + ': ' + group.stones +
        ' Steine, ' + group.liberties + ' Freiheiten, Stärke ' +
        group.strength.toFixed(2) + marks;
      article.appendChild(line);
    });

    resultsList.appendChild(article);
  }

  // Brettfoto statt SGF: als Formulardaten senden, damit der Server das
  // Bild am Feldnamen erkennt.
  function submitImage(file) {
    var body = new FormData();
    body.append('image', file, file.name || 'brett.png');

    busy(true);

    return fetch('/analyze?' + queryFromOptions().toString(), {
      method: 'POST',
      body: body
    }).then(function (res) {
      return res.json().then(function (payload) {
        if (!res.ok) {
          throw new Error(payload.error || ('HTTP ' + res.status));
        }

        lastReport = payload;
        render(payload);
        setStatus('Stellung erkannt.', false);
      });
    });
  }

  form.addEventListener('submit', function (ev) {
    ev.preventDefault();
    setStatus('', false);

    if (imageInput && imageInput.files && imageInput.files.length > 0) {
      submitImage(imageInput.files[0]).catch(function (err) {
        setStatus('Fehler: ' + err.message, true);
      }).then(function () {
        busy(false);
      });

      return;
    }

    readSGF().then(function (sgf) {
      var ogsField = form.elements.namedItem('ogs');
      var ogsRef = ogsField && ogsField.value ? ogsField.value.trim() : '';

      if (!sgf && !ogsRef) {
        setStatus('Bitte SGF-Datei wählen, SGF-Text einfügen ' +
          'oder eine OGS-Partie angeben.', true);

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
