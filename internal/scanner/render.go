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

// Start prints the header and first progress bar, then begins
// overwriting the bar every interval. Returns immediately.
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

	// Save cursor after the progress bar line.
	out.SaveCursorPosition()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-ticker.C:
				// Move up 1 line, clear, reprint bar.
				out.CursorUp(1)
				out.ClearLine()
				fmt.Fprintln(t.out, t.prog.Render())
				out.SaveCursorPosition()
			}
		}
	}()
}

// InsertAbove writes a block of text above the sticky progress bar.
func (t *Ticker) InsertAbove(block string) {
	if block == "" {
		return
	}
	if !t.tty {
		fmt.Fprint(t.out, block)
		return
	}

	out := termenv.NewOutput(t.out.(*os.File))

	// Move up 1 line (the progress bar).
	out.CursorUp(1)
	out.ClearLine()

	// Print the found block.
	fmt.Fprint(t.out, block)

	// Reprint the progress bar below.
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
}

// Header returns the static config line printed once at the start of
// a scan.
func (p *Progress) Header() string {
	return fmt.Sprintf("[scan] ports=%d-%d  qps=%d  workers=%d  timeout=%s",
		p.PortsLo, p.PortsHi, p.QPS, p.Workers, p.Timeout)
}

// Render returns one status line.
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
	filled := int(float64(width) * pct / 100)
	if filled > width {
		filled = width
	}
	filledChar, emptyChar := "█", "░"
	if p.Styles != nil && p.Styles.ascii {
		filledChar, emptyChar = "#", "."
	}
	bar := p.Styles.BarFilled.Render(strings.Repeat(filledChar, filled)) +
		p.Styles.BarEmpty.Render(strings.Repeat(emptyChar, width-filled))

	return fmt.Sprintf("%s  %s  %s  ETA %s  %s",
		bar,
		p.Styles.Pct.Render(fmt.Sprintf("%5.1f%%", pct)),
		p.Styles.Meta.Render(fmt.Sprintf("(%d/%d)", probed, p.Total)),
		p.Styles.Eta.Render(formatDuration(eta)),
		p.Styles.Pps.Render(fmt.Sprintf("%.0f pps", pps)),
	)
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
