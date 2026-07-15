# revu

Ein TUI-Code-Review-Tool mit VIM-Keybindings. Reviewt lokale (gestagte)
Änderungen **bevor** sie committed sind — und merkt sich das, sodass beim
späteren PR-Review nichts doppelt reviewt werden muss.

## Installation

```sh
cd revu
go build -o revu .
cp revu /usr/local/bin/   # oder: go install
```

Voraussetzungen: `git`, für die PR-View ein authentifiziertes `gh`.

## Views

Oben links sind immer alle Views sichtbar; die aktive steht in Klammern
(z.B. `[LOCAL] · COMMITS · PR · FILES`). Mit `[`/`]` wird durchgewechselt.
Oben rechts zeigt eine Prozentanzeige den Review-Fortschritt der aktiven
View (LOCAL: gestagte Zeilen, COMMITS: alle gelisteten Commits bzw. der
geöffnete Commit, PR: der PR-Diff, FILES: Anteil markierter Dateien) —
grau bei 0 %, orange dazwischen, blau bei 100 %.

- **LOCAL** (`1`): Gestagte + ungestagte + untracked Dateien des Working
  Trees. Nur gestagte Hunks/Zeilen sind als reviewed markierbar.
- **COMMITS** (`2`): Bei offenem PR alle Commits des PRs
  (`origin/<base>..HEAD`, inkl. lokaler noch ungepushter obendrauf);
  sonst die Commits vor dem Upstream (Fallback: die letzten 25).
  `space` markiert einen ganzen Commit als reviewed — d.h. alle
  Zeilenänderungen des Commits. `enter` öffnet den Dateibaum des Commits,
  in dem man einzelne Dateien und Hunks/Zeilen reviewen kann (`esc` führt
  zurück zur Commit-Liste).
- **PR** (`3`): Der Diff des offenen PRs für den aktuellen Branch.
  Alles ist markierbar.
- **FILES** (`4`): Der komplette Dateibaum des Repos (tracked +
  untracked, `.gitignore` wird beachtet). Alle Ordner starten
  eingeklappt; nur ein Top-Level-Ordner `app` wird, falls vorhanden,
  automatisch aufgeklappt. Jede Datei ist eine Review-Einheit; rechts
  wird ihr Inhalt als Vorschau angezeigt.
  `space` markiert die **aktuelle Version** der Datei als reviewed —
  und damit logischerweise auch alle Änderungen, die zu ihr geführt
  haben: die Zeilen aller früheren Commits an dieser Datei (plus
  aktuelle staged/unstaged Änderungen) gelten in den anderen Views
  ebenfalls als reviewed. Alles, was **danach** geändert wird, bekommt
  neue IDs und ist wieder un-reviewed; auch die Datei-Markierung selbst
  hasht Pfad + Inhalt und verfällt bei der nächsten Änderung. Erneutes
  `space` entfernt die Markierung samt der History-Marks wieder.

Neben „reviewed" (`space`) gibt es „überflogen" (`S` oder `ctrl+space`):
ich habe den Code gelesen und er ergibt Sinn, aber ich habe ihn nicht
selbst geschrieben. Überflogene Zeilen zählen wie reviewte in den
Fortschritt und in `revu check`, werden aber violett statt blau
angezeigt. `space` auf überflogenen Zeilen stuft sie zu reviewed hoch,
`S` auf reviewten Zeilen stuft sie zu überflogen herab; die zweite
gleiche Taste entfernt die Markierung wieder.

> **Warum nicht Shift+Space?** Terminals senden für Shift+Space exakt
> dasselbe Byte wie für Space — die Kombination ist für ein TUI nicht
> unterscheidbar (dafür bräuchte es das Kitty-Keyboard-Protokoll, das
> Bubble Tea v1 nicht unterstützt). `ctrl+space` kommt dem am nächsten.

Review-Markierungen sind content-adressiert (Hash aus Pfad + Zeileninhalt).
Eine Zeile, die im gestagten Zustand reviewt wurde, bleibt nach Commit und
Push im PR-Diff reviewed. Der State liegt in `.revu/reviewed.json` im Repo
(ignoriert sich selbst über `.revu/.gitignore`).

## Farben

| Zustand              | Farbe                |
| -------------------- | -------------------- |
| gestaged             | grün `#29D398`       |
| reviewed             | blau `#26BBD9`       |
| überflogen (skimmed) | violett `#B877DB`    |
| unstaged / untracked / Kontext | grau/weiß `#ECEFF4` |
| teilweise reviewed   | orange `#FAB795`     |
| entfernte Zeilen (−), staged | rot `#E95678` |
| entfernte Zeilen (−), unstaged | blasses rot `#F8CCD6` |
| hinzugefügte Zeilen (+), unstaged | blasses grün `#C8F2E3` |

Reviewed gewinnt: eine reviewte Minus-Zeile wird blau statt rot.
Unstaged Änderungen haben nur einen leichten Farb-Tint, damit staged
Änderungen klar hervorstechen.

Dateien im Baum: grün = gestaged, orange = teilweise reviewt,
blau = komplett reviewt, grau = nur unstaged/untracked.

## Keybindings

### Global

| Taste       | Aktion                                        |
| ----------- | --------------------------------------------- |
| `[` / `]`   | Durch die Views wechseln (LOCAL / COMMITS / PR / FILES) |
| `1` `2` `3` `4` | Direkt zu einer View springen             |
| `J` / `K` (shift) | Diff-Fenster scrollen (aus jeder View) |
| `ctrl+d` / `ctrl+u` | Diff halbe Seite scrollen (aus jeder View, auch PgDn/PgUp) |
| `ctrl+o`    | Review-Prompt kopieren (im Diff mit Zeilenbereich) |
| `H`         | Syntax-Highlighting im Diff togglen           |
| `/`         | Suchen (enter: bestätigen, esc: abbrechen)    |
| `n` / `N`   | Nächster / vorheriger Treffer                 |
| `<` / `>`   | An den Anfang / ans Ende springen             |
| `{` / `}`   | Diff-Kontext um eine Zeile verkleinern / vergrößern |
| `+`         | Aktives Fenster zwischen Fullscreen/Split togglen |
| `e`         | Datei im `$EDITOR` öffnen (springt zur Zeile) |
| `r`         | Neu laden                                     |
| `?`         | Keybinding-Popup                              |
| `q` / `ctrl+c` | Beenden                                    |

### Dateibaum (links)

| Taste     | Aktion                                  |
| --------- | ---------------------------------------- |
| `j` / `k` | Navigieren (Diff-Vorschau folgt)         |
| `h` / `l` | Ordner ein-/ausklappen, `h` springt zum Elternordner |
| `enter`   | Ordner togglen bzw. Datei öffnen (fokussiert Diff) |
| `space`   | Ganze Datei als reviewed togglen         |
| `S` / `ctrl+space` | Ganze Datei als überflogen togglen |
| `m`       | Markier-Menü für Datei **oder Ordner** öffnen |
| `s`       | Datei / Ordner stagen bzw. unstagen (Toggle) |
| `g` / `G` | Anfang / Ende                            |

### Commit-Liste (links, COMMITS-View)

| Taste     | Aktion                                   |
| --------- | ---------------------------------------- |
| `j` / `k` | Navigieren (Diff-Vorschau folgt)         |
| `enter`   | Dateibaum des Commits öffnen (`esc`: zurück) |
| `space`   | Ganzen Commit als reviewed togglen       |
| `S` / `ctrl+space` | Ganzen Commit als überflogen togglen |

### Diff (rechts)

| Taste     | Aktion                                   |
| --------- | ----------------------------------------- |
| `j` / `k` | Durch Hunks (bzw. Zeilen) gehen           |
| `a`       | Zwischen Hunk- und Zeilen-Modus togglen   |
| `v`       | Multi-Line-Select (Visual Mode)           |
| `space`   | Hunk / Zeile / Auswahl als reviewed togglen |
| `S` / `ctrl+space` | Hunk / Zeile / Auswahl als überflogen togglen |
| `s`       | Hunk / Zeile / Auswahl stagen bzw. unstagen (Toggle) |
| `esc`     | Visual verlassen (zurück in Hunk-Modus) bzw. zurück zum Dateibaum |
| `g` / `G` | Anfang / Ende                             |

## Diff-Fenster

Links läuft eine Zeilennummern-Spalte mit: Kontext- und `+`-Zeilen
zeigen die Zeilennummer der neuen Datei, `-`-Zeilen die der alten.
Die Nummern sind immer im Zustand der Zeile eingefärbt (grün gestaged,
rot entfernt, blau reviewed, violett überflogen …) — unabhängig vom
Syntax-Highlighting.

`H` toggled Syntax-Highlighting für den Code (via
[chroma](https://github.com/alecthomas/chroma), Lexer nach Dateiendung;
standardmäßig an). Ist es aktiv, wird der Zeileninhalt nach Sprache
eingefärbt und nur der `+`/`-`-Marker links (plus die Zeilennummern)
trägt die Diff-/Review-Farbe. Die Zeile unter dem Cursor, Visual-
Auswahlen und Suchtreffer fallen auf die klassische Diff-Färbung
zurück, damit Auswahl und Treffer sichtbar bleiben. `H` schaltet
komplett auf die alte Volltext-Färbung zurück.

Rechts am Rand zeigt eine Scrollbar Position und Größe des sichtbaren
Ausschnitts, sobald der Diff nicht komplett auf den Schirm passt. Mit
dem Mausrad wird gescrollt: über dem Diff frei (3 Zeilen pro Tick),
über der linken Liste bewegt es die Auswahl.

## Suche

`/` öffnet die Suchleiste — sie sucht dort, wo der Fokus gerade ist:
im Dateibaum (über Pfade, klappt zugeklappte Ordner automatisch auf),
in der Commit-Liste (Hash + Subject) und im Diff. Treffer werden mit
lila Hintergrund (`#B877DB`) hervorgehoben, schon während des Tippens;
der Treffer, auf dem man gerade steht, bekommt ein dunkleres Lila
(`#8A3FB8`).
`enter` schließt die Leiste (Suche bleibt aktiv für `n`/`N`), `esc`
bricht ab und entfernt alle Highlights.

## Dateibaum

Jede Datei trägt einen Status-Buchstaben: `M` modified, `A` added
(auch untracked), `D` deleted. Hat eine Datei gleichzeitig gestagte und
ungestagte Änderungen, steht ein zusätzliches `M` davor (`MM datei.ts`).

Ordner färben sich nach ihrem aggregierten Inhalt: blau wenn alles
darin reviewt ist, orange wenn nur ein Teil, sonst weiß (`#ECEFF4`).

## Review-Prompt (`ctrl+o`)

`ctrl+o` kopiert einen Prompt in die Zwischenablage, z.B. für eine KI
oder einen Kollegen:

```
review diese änderungen:
Datei: src/foo.go
Zeilen: 10-14
```

Im Diff wird der Zeilenbereich der aktuellen Auswahl (Hunk, Zeile oder
Visual-Range) mitgenommen; im Dateibaum entfällt die `Zeilen:`-Angabe.

## Markier-Menü (`m`) & permanente Markierungen

`space`/`S` bleiben die Schnell-Markierungen für einzelne Dateien. `m`
öffnet auf der ausgewählten Datei **oder einem Ordner** ein Popup mit
vier Optionen (`j`/`k` wählen, `enter` bestätigt, `esc` bricht ab):

1. **reviewed** — wie `space`; bei einem Ordner für alle Dateien
   darunter (alles gesetzt → alles wieder entfernt).
2. **skimmed** — wie `S`, ebenfalls auch für Ordner.
3. **permanently reviewed** — pfadbasiert statt content-adressiert:
   Datei bzw. der ganze Ordner gilt dauerhaft als reviewed, **auch nach
   künftigen Änderungen**. Für generierte Dateien/Ordner, die implizit
   reviewed sind (Lockfiles, Snapshots, Vendor-Code …).
4. **permanently skimmed** — dasselbe als überflogen (violett). Für
   Dateien/Ordner mit begrenztem Impact, die man bedenkenlos
   durchwinken kann.

Permanent markierte Dateien und Ordner tragen im Baum ein `∞` hinter
dem Namen — auch alles, was die Markierung von einem Elternordner erbt.

Das Menü ist ein Single-Select: Ein `✓` zeigt den einen gerade aktiven
Zustand (permanente Marks überdecken dabei Content-Marks). Die gleiche
Option erneut bestätigen entfernt die Markierung; eine andere wählen
wechselt den Zustand (z.B. ersetzt „reviewed" eine permanente
Markierung). Permanente Markierungen
wirken überall (Baum, Prozentanzeige, Diff-Färbung, `revu check`) und
liegen als Pfade in `.revu/reviewed.json`
(`permanentReviewed`/`permanentSkimmed`).

## Dateien vom Review ausschließen

Standardmäßig sind alle Dateien namens `snapshot.json` vom Review
ausgenommen: sie zählen nicht in die Prozentanzeige, färben keine Ordner
und lassen sich nicht als reviewed markieren (sie werden gedimmt
angezeigt). Eigene Muster in `.revu/config.json` **ersetzen** den
Default:

```json
{ "exclude": ["snapshot.json", "*.lock", "generated/*"] }
```

Muster ohne `/` matchen den Dateinamen (Glob), Muster mit `/` den Pfad
relativ zur Repo-Wurzel. Änderungen greifen beim nächsten Reload (`r`).

## `revu check` (Pre-Commit-Gate)

`revu check` prüft ohne UI, ob **alle gestagten Zeilen reviewt** sind:
Exit-Code 0 bei 100 % (oder wenn nichts gestaged ist), sonst Exit-Code 1
mit einer Liste der unreviewten Dateien. Excludes aus `.revu/config.json`
zählen nicht mit. Gedacht für Pre-Commit-Hooks:

```sh
if ! revu check; then exit 1; fi
```

## Binärdateien

Binärdateien (Bilder etc.) zählen als eine Review-Einheit: `space`
toggled die ganze Datei — im Baum wie im Diff (dort steht
`(binary file)`). Die Review-ID hasht Pfad + Blob-Hash; ändert sich die
Datei, gilt sie wieder als un-reviewed, und Marks überleben von staged
bis in den PR-Diff.

## Hinweise

- Hunks kommen 1:1 aus `git diff` (Unified Format). revu startet mit
  1 Kontextzeile (`-U1`), damit Hunks klein bleiben — git verschmilzt
  Änderungsblöcke, deren Kontexte sich überlappen, zu einem Hunk. Mit
  `{`/`}` lässt sich der Kontext live ändern; Review-Markierungen bleiben
  dabei erhalten (IDs hashen nur geänderte Zeilen, nie Kontext).
- In der PR-View wird der Diff dafür lokal gegen `origin/<base>`
  berechnet; ist die Base-Ref lokal nicht vorhanden, fällt revu auf
  `gh pr diff` zurück (dann fix 3 Kontextzeilen).

- Wird eine reviewte Zeile nachträglich geändert, ändert sich ihr Hash —
  sie gilt wieder als un-reviewed. Das ist Absicht.
- `.revu/` kann jederzeit gelöscht werden, um alle Markierungen zu
  verwerfen.
