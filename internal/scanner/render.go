package scanner

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/renanbray/swoomps/internal/model"
)

// Styles groups every lipgloss style used by the live output. Two
// variants are constructed by NewStyles: a colorful one for the
// default mode and a plain one for the --ascii flag (no colors,
// ASCII border, plain block characters).
type Styles struct {
	ascii bool

	IPActive    lipgloss.Style
	IPIdle      lipgloss.Style
	FoundPrefix lipgloss.Style
	Card        lipgloss.Style
	Label       lipgloss.Style
	Value       lipgloss.Style
	BarFilled   lipgloss.Style
	BarEmpty    lipgloss.Style
	Pct         lipgloss.Style
	Meta        lipgloss.Style
	Eta         lipgloss.Style
	Pps         lipgloss.Style
}

// NewStyles returns the palette for the given mode. ascii=true drops
// all colors and switches the progress bar to '#'/'.' and the card
// border to ASCII characters.
func NewStyles(ascii bool) *Styles {
	s := &Styles{ascii: ascii}
	if ascii {
		s.IPActive = lipgloss.NewStyle().Bold(true)
		s.IPIdle = lipgloss.NewStyle().Bold(true)
		s.FoundPrefix = lipgloss.NewStyle().Bold(true)
		s.Card = lipgloss.NewStyle().Border(lipgloss.ASCIIBorder()).Padding(0, 1).MarginLeft(4)
		s.Label = lipgloss.NewStyle()
		s.Value = lipgloss.NewStyle()
		s.BarFilled = lipgloss.NewStyle()
		s.BarEmpty = lipgloss.NewStyle()
		s.Pct = lipgloss.NewStyle().Bold(true)
		s.Meta = lipgloss.NewStyle()
		s.Eta = lipgloss.NewStyle()
		s.Pps = lipgloss.NewStyle()
		return s
	}
	s.IPActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	s.IPIdle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
	s.FoundPrefix = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	s.Card = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		MarginLeft(4)
	s.Label = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.Value = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	s.BarFilled = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	s.BarEmpty = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	s.Pct = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	s.Meta = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.Eta = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	s.Pps = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	return s
}

// FormatFound returns a styled "found" block as a string. The address
// sits on its own line; the metadata follows in a bordered card.
func (s *Styles) FormatFound(srv model.Server) string {
	var b strings.Builder

	ipStyle := s.IPIdle
	if srv.NumPlayers > 0 {
		ipStyle = s.IPActive
	}

	fmt.Fprint(&b, s.FoundPrefix.Render("[+] "))
	fmt.Fprint(&b, ipStyle.Render(srv.Address()))
	fmt.Fprintln(&b)

	var rows []string

	var r1 strings.Builder
	r1.WriteString(s.Label.Render("Version:  "))
	r1.WriteString(s.Value.Render(srv.VersionName))
	if srv.VersionProtocol != 0 {
		r1.WriteString(s.Label.Render("  (proto "))
		r1.WriteString(s.Value.Render(fmt.Sprintf("%d", srv.VersionProtocol)))
		r1.WriteString(s.Label.Render(")"))
	}
	r1.WriteString(s.Label.Render("    Players:  "))
	r1.WriteString(s.Value.Render(fmt.Sprintf("%d/%d", srv.NumPlayers, srv.MaxPlayers)))
	r1.WriteString(s.Label.Render("    Latency:  "))
	r1.WriteString(s.Value.Render(fmt.Sprintf("%dms", srv.LastLatencyMs)))
	if srv.ProbeKind != "" {
		r1.WriteString(s.Label.Render("    Kind:  "))
		r1.WriteString(s.Value.Render(srv.ProbeKind))
	}
	rows = append(rows, r1.String())

	if srv.MOTD != "" {
		motd := srv.MOTD
		if len(motd) > 70 {
			motd = motd[:67] + "..."
		}
		var r2 strings.Builder
		r2.WriteString(s.Label.Render("MOTD:     "))
		r2.WriteString(s.Value.Render(motd))
		rows = append(rows, r2.String())
	}

	content := strings.Join(rows, "\n")
	fmt.Fprintln(&b, s.Card.Render(content))
	fmt.Fprintln(&b)
	return b.String()
}

// Ticker manages a sticky progress bar at the bottom of the terminal.
// Found-server blocks are inserted above the bar via InsertAbove.
// Updates overwrite the bar in place using ANSI cursor control.
type Ticker struct {
	prog *Progress
	out  io.Writer
	done chan struct{}

	// tty is true when stdout is a terminal. When false, ANSI
	// cursor sequences are skipped and output scrolls normally.
	tty bool
}

// NewTicker creates a Ticker. Call Start to begin rendering.
func NewTicker(prog *Progress) *Ticker {
	w := prog.Out
	if w == nil {
		w = os.Stdout
	}
	_, isTTY := w.(*os.File)
	return &Ticker{prog: prog, out: w, done: make(chan struct{}), tty: isTTY}
}

// Start prints the header and first progress output, then begins
// overwriting the two progress lines every interval. Returns immediately.
func (t *Ticker) Start(interval time.Duration) {
	if !t.tty {
		fmt.Fprintln(t.out, t.prog.Header())
		fmt.Fprintln(t.out, t.prog.Render())
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-t.done:
					return
				case <-ticker.C:
					fmt.Fprintln(t.out, t.prog.Render())
				}
			}
		}()
		return
	}

	out := termenv.NewOutput(t.out.(*os.File))

	fmt.Fprintln(t.out, t.prog.Header())
	fmt.Fprintln(t.out, t.prog.Render())

	// Save cursor after the two progress lines.
	out.SaveCursorPosition()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-ticker.C:
				// Move up 2 lines, clear both, reprint.
				out.CursorUp(1)
				out.ClearLine()
				out.CursorUp(1)
				out.ClearLine()
				fmt.Fprintln(t.out, t.prog.Render())
				out.SaveCursorPosition()
			}
		}
	}()
}

// InsertAbove writes a block of text above the sticky progress lines.
func (t *Ticker) InsertAbove(block string) {
	if block == "" {
		return
	}
	if !t.tty {
		fmt.Fprint(t.out, block)
		return
	}

	out := termenv.NewOutput(t.out.(*os.File))

	// Move up 2 lines and clear both.
	out.CursorUp(1)
	out.ClearLine()
	out.CursorUp(1)
	out.ClearLine()

	// Print the found block.
	fmt.Fprint(t.out, block)

	// Reprint the two progress lines below.
	fmt.Fprintln(t.out, t.prog.Render())
	out.SaveCursorPosition()
}

// Stop halts the background goroutine. The last progress bar line
// remains visible.
func (t *Ticker) Stop() {
	close(t.done)
}

// Progress tracks the live state of a scan and renders the status
// line the user sees while the run is in progress.
type Progress struct {
	Total   int64
	Probed  *atomic.Int64
	Found   *atomic.Int64
	Errors  *atomic.Int64
	Started time.Time

	PortsLo uint16
	PortsHi uint16
	QPS     int
	Workers int
	Timeout time.Duration

	Out    io.Writer
	Styles *Styles

	// State is the shared progress state updated by the generator
	// and workers. The renderer only reads from it.
	State *ProgressState
}

// Header returns the static config line printed once at the start of
// a scan.
func (p *Progress) Header() string {
	return fmt.Sprintf("[scan] ports=%d-%d  qps=%d  workers=%d  timeout=%s",
		p.PortsLo, p.PortsHi, p.QPS, p.Workers, p.Timeout)
}

// Render returns two lines: per-IP progress bar and overall progress bar.
func (p *Progress) Render() string {
	probed := p.Probed.Load()
	if probed > p.Total {
		probed = p.Total
	}
	pct := 0.0
	if p.Total > 0 {
		pct = float64(probed) / float64(p.Total) * 100
	}
	elapsed := time.Since(p.Started)
	pps := 0.0
	if elapsed > 0 {
		pps = float64(probed) / elapsed.Seconds()
	}
	var eta time.Duration
	if pps > 0 {
		eta = time.Duration(float64(p.Total-probed)/pps*float64(time.Second))
	}

	const width = 30
	filledChar, emptyChar := "█", "░"
	if p.Styles != nil && p.Styles.ascii {
		filledChar, emptyChar = "#", "."
	}

	// --- overall bar (line 2) ---
	filled := int(float64(width) * pct / 100)
	if filled > width {
		filled = width
	}
	overallBar := p.Styles.BarFilled.Render(strings.Repeat(filledChar, filled)) +
		p.Styles.BarEmpty.Render(strings.Repeat(emptyChar, width-filled))

	line2 := fmt.Sprintf("%s  %s  %s  ETA %s  %s",
		overallBar,
		p.Styles.Pct.Render(fmt.Sprintf("%5.1f%%", pct)),
		p.Styles.Meta.Render(fmt.Sprintf("(%s)", formatCount(probed)+"/"+formatCount(p.Total))),
		p.Styles.Eta.Render(formatDuration(eta)),
		p.Styles.Pps.Render(fmt.Sprintf("%.0f pps", pps)),
	)

	// --- per-IP bar (line 1) ---
	seedTotal := int64(p.PortsHi - p.PortsLo + 1)
	var ipPct float64
	var ipScanned int64
	if p.State != nil {
		ipScanned = p.State.CurrentScanned.Load()
		if ipScanned > seedTotal {
			ipScanned = seedTotal
		}
		if seedTotal > 0 {
			ipPct = float64(ipScanned) / float64(seedTotal) * 100
		}
	}

	ipFilled := int(float64(width) * ipPct / 100)
	if ipFilled > width {
		ipFilled = width
	}
	ipBar := p.Styles.BarFilled.Render(strings.Repeat(filledChar, ipFilled)) +
		p.Styles.BarEmpty.Render(strings.Repeat(emptyChar, width-ipFilled))

	var seedLabel string
	if p.State != nil {
		// Prefer the per-probe Current (IP:Port) when available;
		// fall back to the seed IP before the first probe lands.
		if cp := p.State.Current.Load(); cp != nil && *cp != "" {
			seedLabel = *cp
		} else if cp := p.State.CurrentSeed.Load(); cp != nil {
			seedLabel = *cp
		}
	}

	line1 := fmt.Sprintf("%s  %s  %s  %s",
		ipBar,
		p.Styles.Pct.Render(fmt.Sprintf("%5.1f%%", ipPct)),
		p.Styles.Meta.Render(fmt.Sprintf("(%s)", formatCount(ipScanned)+"/"+formatCount(seedTotal))),
		p.Styles.Meta.Render("scanning: ")+p.Styles.Value.Render(seedLabel),
	)

	return line1 + "\n" + line2
}

// formatDuration turns a duration into a compact ETA-style string:
// "0:42", "1:23", "2:05:00". Non-positive renders as "0:00".
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0:00"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// ProgressState tracks the live state shared between the target
// generator (writer for seed-level state), worker pool (writer for
// the per-probe field), and the progress renderer (reader of all).
//
// The generator owns CurrentSeed and resets CurrentScanned to 0 on
// each new seed. Workers own Current and increment CurrentScanned
// after each completed probe. Only the renderer reads these.
// Atomic.Pointer is used to avoid copying the string on every write.
type ProgressState struct {
	// Current is the IP:Port currently being probed. Updated by
	// workers before each probe; read by the progress renderer.
	Current atomic.Pointer[string]

	// CurrentSeed is the IP whose ports are being scanned right now.
	// Written by the target generator when a new seed starts.
	CurrentSeed atomic.Pointer[string]

	// CurrentScanned counts probes completed for CurrentSeed.
	// Reset to 0 by the generator on seed change; incremented by
	// workers after each probe.
	CurrentScanned atomic.Int64
}

// NewProgressState returns an empty ProgressState. Safe for zero value
// use, but constructing explicitly documents ownership.
func NewProgressState() *ProgressState {
	return &ProgressState{}
}

// FormatCount turns a number into a compact string: 1234 → "1234",
// 1234567 → "1.2M", 268000000 → "268M".
func formatCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fG", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
