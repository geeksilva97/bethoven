// Package cardimg renders a player's end-of-tournament card to a PNG — a real,
// shareable image (the pool posts them on Discord) drawn straight from the
// service's computed PlayerCard. Pure Go (no cgo): it uses fogleman/gg over the
// Go Regular/Bold fonts embedded in golang.org/x/image, so the binary stays
// self-contained with no external font assets.
//
// Color emoji (🥇🤖) don't render in Go's text stack, so a top-three finish is
// drawn as a gold/silver/bronze RING BADGE around the rank number rather than a
// medal glyph; everything else is plain embedded-font text.
package cardimg

import (
	"bytes"
	"fmt"
	"image/color"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"bethoven/internal/service"
)

// Canvas geometry. 1000×630 is a comfortable share aspect (close to 1.91:1, the
// Discord/OpenGraph card ratio) and leaves room for the narrative.
const (
	width  = 1000
	height = 630
	margin = 40
)

// Palette — mirrors the terminal's calm gold-on-dark look (internal/tui/styles.go).
var (
	colBG      = mustHex("#0f1117")
	colPanel   = mustHex("#161922")
	colText    = mustHex("#f2f3f5")
	colDim     = mustHex("#8a909c")
	colGreen   = mustHex("#3fb950")
	colRed     = mustHex("#ff6b6b")
	colGold    = mustHex("#d4af37")
	colSilver  = mustHex("#c0c0c0")
	colBronze  = mustHex("#cd7f32")
	colNeutral = mustHex("#6b7280")
)

// regular/bold are the parsed embedded fonts, loaded once.
var (
	regular = mustParse(goregular.TTF)
	bold    = mustParse(gobold.TTF)
)

// Render draws the card and returns PNG bytes. It never fails on the drawing
// itself (all inputs are already validated by the service); the error is reserved
// for PNG encoding.
func Render(c service.PlayerCard) ([]byte, error) {
	dc := gg.NewContext(width, height)

	// Background + a rounded panel with a border tinted by podium finish.
	dc.SetColor(colBG)
	dc.Clear()
	accent := medalColor(c.Medal)
	dc.DrawRoundedRectangle(margin/2, margin/2, width-margin, height-margin, 22)
	dc.SetColor(colPanel)
	dc.FillPreserve()
	dc.SetLineWidth(4)
	dc.SetColor(accent)
	dc.Stroke()

	drawBadge(dc, c, accent)
	drawHeader(dc, c, accent)
	y := drawStats(dc, c)
	y = drawTrajectory(dc, c, accent, y)
	y = drawPicks(dc, c, y)
	drawNarrative(dc, c, y)
	drawFooter(dc)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("encode card png: %w", err)
	}
	return buf.Bytes(), nil
}

// drawBadge draws the ring badge: a thick ring in the podium colour with the
// final rank number centred — the emoji-free stand-in for a medal.
func drawBadge(dc *gg.Context, c service.PlayerCard, accent color.Color) {
	cx, cy, r := float64(margin+62), float64(margin+62), 50.0
	dc.SetLineWidth(9)
	dc.SetColor(accent)
	dc.DrawCircle(cx, cy, r)
	dc.Stroke()
	dc.SetFontFace(face(bold, 44))
	dc.SetColor(colText)
	dc.DrawStringAnchored(fmt.Sprintf("%d", c.FinalRank), cx, cy-2, 0.5, 0.5)
}

// drawHeader writes the player's name, finishing place, and the tournament line
// to the right of the badge.
func drawHeader(dc *gg.Context, c service.PlayerCard, accent color.Color) {
	x := float64(margin + 132)
	dc.SetColor(colText)
	dc.SetFontFace(face(bold, 46))
	dc.DrawStringAnchored(truncateToWidth(dc, c.User.DisplayName, float64(width)-x-margin), x, float64(margin+40), 0, 0.5)

	dc.SetColor(accent)
	dc.SetFontFace(face(bold, 26))
	dc.DrawStringAnchored(place(c.FinalRank), x, float64(margin+82), 0, 0.5)

	dc.SetColor(colDim)
	dc.SetFontFace(face(regular, 18))
	dc.DrawStringAnchored("BEThoven · FIFA World Cup 2026", x, float64(margin+110), 0, 0.5)
}

// drawStats writes the one-line metric row (only the parts that say something) and
// returns the y baseline below it for the next section.
func drawStats(dc *gg.Context, c service.PlayerCard) float64 {
	y := float64(margin + 168)
	x := float64(margin + 24)
	parts := []string{
		fmt.Sprintf("%d pts", c.Total),
		fmt.Sprintf("%d exact", c.ExactHits),
		fmt.Sprintf("%d correct", c.CorrectResults),
	}
	if c.MatchesBet > 0 {
		parts = append(parts, fmt.Sprintf("%d%% hit rate", c.HitRate))
	}
	if c.BestStreak >= 2 {
		parts = append(parts, fmt.Sprintf("%d streak", c.BestStreak))
	}
	dc.SetFontFace(face(bold, 24))
	dc.SetColor(colText)
	line := joinDot(parts)
	dc.DrawStringAnchored(line, x, y, 0, 0.5)
	return y + 46
}

// drawTrajectory draws the rank arc as a row of bars (taller = better rank) with a
// "start → final, peak" caption, and returns the y baseline below it.
func drawTrajectory(dc *gg.Context, c service.PlayerCard, accent color.Color, y float64) float64 {
	x := float64(margin + 24)
	dc.SetFontFace(face(regular, 18))
	dc.SetColor(colDim)
	dc.DrawStringAnchored("Trajectory", x, y, 0, 0.5)

	caption := fmt.Sprintf("%s → %s", ordinal(c.StartRank), ordinal(c.FinalRank))
	if c.PeakRank > 0 {
		caption += fmt.Sprintf(", peak %s", ordinal(c.PeakRank))
	}

	bars := c.Trajectory
	if len(bars) == 0 {
		dc.DrawStringAnchored(caption, x+130, y, 0, 0.5)
		return y + 40
	}
	// Cap to the most recent points that fit, so a long tournament still reads.
	const maxBars = 40
	if len(bars) > maxBars {
		bars = bars[len(bars)-maxBars:]
	}
	// Scale by position: best (min) position is the tallest bar.
	minPos, maxPos := bars[0].Position, bars[0].Position
	for _, b := range bars {
		if b.Position < minPos {
			minPos = b.Position
		}
		if b.Position > maxPos {
			maxPos = b.Position
		}
	}
	areaX, areaY := x+130.0, y-22.0
	areaW, areaH := 380.0, 30.0
	bw := areaW / float64(len(bars))
	gap := 2.0
	dc.SetColor(accent)
	for i, b := range bars {
		frac := 1.0 // single distinct value ⇒ full height
		if maxPos > minPos {
			frac = float64(maxPos-b.Position) / float64(maxPos-minPos)
		}
		h := 6 + frac*areaH // floor so even the worst bar is visible
		bx := areaX + float64(i)*bw
		dc.DrawRectangle(bx, areaY+areaH-h+8, bw-gap, h)
		dc.Fill()
	}
	dc.SetFontFace(face(regular, 18))
	dc.SetColor(colDim)
	dc.DrawStringAnchored(caption, areaX+areaW+18, y, 0, 0.5)
	return y + 44
}

// drawPicks writes the best-call (green) and worst-miss (red) lines, when present,
// and returns the y baseline below them.
func drawPicks(dc *gg.Context, c service.PlayerCard, y float64) float64 {
	x := float64(margin + 24)
	dc.SetFontFace(face(regular, 20))
	if c.BestPick != nil {
		dc.SetColor(colGreen)
		dc.DrawStringAnchored("Best call", x, y, 0, 0.5)
		dc.SetColor(colText)
		dc.DrawStringAnchored(pickText(c.BestPick), x+120, y, 0, 0.5)
		y += 32
	}
	if c.WorstPick != nil {
		dc.SetColor(colRed)
		dc.DrawStringAnchored("Worst miss", x, y, 0, 0.5)
		dc.SetColor(colText)
		dc.DrawStringAnchored(pickText(c.WorstPick), x+120, y, 0, 0.5)
		y += 32
	}
	return y + 14
}

// drawNarrative wraps BETanIA's "hero's journey" text (when present) into the
// remaining space.
func drawNarrative(dc *gg.Context, c service.PlayerCard, y float64) {
	if c.Narrative == "" {
		return
	}
	x := float64(margin + 24)
	dc.SetFontFace(face(regular, 20))
	dc.SetColor(colDim)
	maxY := float64(height - margin - 40)
	if y > maxY {
		return
	}
	dc.DrawStringWrapped(c.Narrative, x, y-6, 0, 0, float64(width)-x-margin-24, 1.35, gg.AlignLeft)
}

// drawFooter writes the small branding line at the bottom of the card.
func drawFooter(dc *gg.Context) {
	dc.SetFontFace(face(regular, 16))
	dc.SetColor(colNeutral)
	dc.DrawStringAnchored("bethoven · played over SSH", float64(width-margin-12), float64(height-margin-2), 1, 0.5)
}

// --- helpers --------------------------------------------------------------------

func medalColor(medal int) color.Color {
	switch medal {
	case 1:
		return colGold
	case 2:
		return colSilver
	case 3:
		return colBronze
	}
	return colNeutral
}

// place names a finishing position, matching the TUI's cardPlace.
func place(rank int) string {
	switch rank {
	case 1:
		return "Champion"
	case 2:
		return "Runner-up"
	case 3:
		return "Third place"
	}
	return ordinal(rank) + " place"
}

// ordinal renders 1→"1st", 2→"2nd", … (English rules), matching tui.cardOrdinal.
func ordinal(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return fmt.Sprintf("%dth", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("%dst", n)
	case 2:
		return fmt.Sprintf("%dnd", n)
	case 3:
		return fmt.Sprintf("%drd", n)
	}
	return fmt.Sprintf("%dth", n)
}

// pickText renders a best-call / worst-miss row. Caller guarantees mr and mr.Bet
// are non-nil (the service sets them only when scored).
func pickText(mr *service.MatchResult) string {
	m := mr.Match
	actual := "?"
	if m.ScoreA != nil && m.ScoreB != nil {
		actual = fmt.Sprintf("%d-%d", *m.ScoreA, *m.ScoreB)
	}
	pred := fmt.Sprintf("%d-%d", mr.Bet.PredA, mr.Bet.PredB)
	if mr.Points > 0 {
		return fmt.Sprintf("%s %s %s  (you said %s, +%d)", m.TeamA, actual, m.TeamB, pred, mr.Points)
	}
	return fmt.Sprintf("%s %s %s  (you said %s)", m.TeamA, actual, m.TeamB, pred)
}

// joinDot joins parts with a middle dot separator.
func joinDot(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "   ·   "
		}
		out += p
	}
	return out
}

// truncateToWidth shortens s with an ellipsis so it fits within maxW pixels at the
// current font face — guards a very long display name from overrunning the card.
func truncateToWidth(dc *gg.Context, s string, maxW float64) string {
	if w, _ := dc.MeasureString(s); w <= maxW {
		return s
	}
	runes := []rune(s)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		if w, _ := dc.MeasureString(string(runes) + "…"); w <= maxW {
			return string(runes) + "…"
		}
	}
	return string(runes)
}

func face(f *truetype.Font, points float64) font.Face {
	return truetype.NewFace(f, &truetype.Options{Size: points})
}

func mustParse(ttf []byte) *truetype.Font {
	f, err := truetype.Parse(ttf)
	if err != nil {
		panic("cardimg: parse embedded font: " + err.Error())
	}
	return f
}

// mustHex parses "#rrggbb" into an opaque colour. Panics on a malformed literal —
// every caller passes a compile-time constant, so a bad value is a programmer bug.
func mustHex(hex string) color.Color {
	var r, g, b uint8
	if n, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil || n != 3 {
		panic("cardimg: bad hex color " + hex)
	}
	return color.RGBA{R: r, G: g, B: b, A: 0xff}
}
