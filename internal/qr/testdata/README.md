# Vergleichsmatrizen

Die Dateien in diesem Verzeichnis stammen aus einer **unabhängigen**
Implementierung — der Python-Bibliothek `qrcode` 8.2 — und sind der Beleg
dafür, dass `internal/qr` das Format trifft: Kodierung, Fehlerkorrektur,
Blockverschränkung, Modulplatzierung, Maskierung und Formatinformation.

Erzeugt mit:

```python
import qrcode
from qrcode import util

q = qrcode.QRCode(error_correction=qrcode.constants.ERROR_CORRECT_M,
                  box_size=1, border=0)
q.add_data(util.QRData(text.encode("utf-8"), mode=util.MODE_8BIT_BYTE))
q.best_fit()
q.makeImpl(False, mask)   # mask 0 bis 7
```

Dateiformat:

```
Zeile 1: die kodierte Zeichenkette
Zeile 2: version=<n>
danach acht Abschnitte "mask=<n>" mit je einer Matrix, '1' dunkel, '0' hell
```

**Weshalb je acht Matrizen und keine „richtige“ darunter.** Alle acht
Masken ergeben ein gültiges, lesbares Symbol; die Norm lässt für die
Bewertung der Masken Auslegungsspielraum, und die Implementierungen nutzen
ihn verschieden. `segno` etwa bewertet ausdrücklich *ohne* eingetragene
Formatinformation, `qrcode` *mit*, und `segno` hängt zudem ein Nullbyte an
den Bitstrom, wo dieser bereits auf der Codewortgrenze endet. Verglichen
wird deshalb je Maske — die Maskenwahl selbst prüft `TestMaskenwahl`
gegen die eigenen Strafregeln.
