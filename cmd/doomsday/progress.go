package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jclement/doomsday/internal/backup"
	"golang.org/x/term"
)

// spinner frames for interactive progress.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// destState tracks progress for a single backup destination.
type destState struct {
	name    string
	stats   backup.Stats
	done    bool
	err     error
	lastLog time.Time // for non-interactive log throttling
}

// multiDestProgress displays live progress for one or more backup destinations.
// On an interactive TTY it renders an updating multi-line table on stderr.
// On non-interactive output it emits periodic log messages.
type multiDestProgress struct {
	mu          sync.Mutex
	dests       []*destState
	rendered    bool // true after first render
	lastRender  time.Time
	termWidth   int
	spinIdx     int
	interactive bool
	maxNameLen  int
}

// newMultiDestProgress creates a progress tracker for multiple destinations.
func newMultiDestProgress(interactive bool) *multiDestProgress {
	w := 80
	if interactive {
		if tw, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && tw > 0 {
			w = tw
		}
	}
	return &multiDestProgress{
		interactive: interactive,
		termWidth:   w,
	}
}

// Register adds a destination and returns its index for Update/Complete calls.
func (p *multiDestProgress) Register(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := len(p.dests)
	p.dests = append(p.dests, &destState{name: name})
	if len(name) > p.maxNameLen {
		p.maxNameLen = len(name)
	}
	return idx
}

// Update records the latest stats for a destination and triggers a render.
func (p *multiDestProgress) Update(idx int, stats backup.Stats) {
	p.mu.Lock()
	p.dests[idx].stats = stats
	p.mu.Unlock()

	if p.interactive {
		p.renderInteractive()
	} else {
		p.renderLog(idx, stats)
	}
}

// Complete marks a destination as finished.
func (p *multiDestProgress) Complete(idx int, err error) {
	p.mu.Lock()
	p.dests[idx].done = true
	p.dests[idx].err = err
	p.mu.Unlock()

	if p.interactive {
		p.renderInteractive()
	}
}

// Clear removes the progress display so subsequent output prints cleanly.
func (p *multiDestProgress) Clear() {
	if !p.interactive || !p.rendered {
		return
	}
	p.mu.Lock()
	n := len(p.dests)
	p.mu.Unlock()

	// Move up and clear each line.
	if n > 1 {
		fmt.Fprintf(os.Stderr, "\033[%dA", n-1)
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", p.termWidth-1))
		if i < n-1 {
			fmt.Fprint(os.Stderr, "\n")
		}
	}
	// Move back up to first line position.
	if n > 1 {
		fmt.Fprintf(os.Stderr, "\033[%dA", n-1)
	}
}

// renderInteractive draws the multi-line table on stderr, throttled to ~200ms.
func (p *multiDestProgress) renderInteractive() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if now.Sub(p.lastRender) < 200*time.Millisecond {
		return
	}
	p.lastRender = now
	p.spinIdx = (p.spinIdx + 1) % len(spinnerFrames)

	n := len(p.dests)

	// Move cursor up to overwrite previous render.
	if p.rendered && n > 1 {
		fmt.Fprintf(os.Stderr, "\033[%dA", n-1)
	}

	for i, d := range p.dests {
		line := p.formatDestLine(d)

		// Truncate to terminal width.
		if len(line) > p.termWidth-1 {
			line = line[:p.termWidth-4] + "..."
		}

		// Pad to clear previous content.
		if pad := p.termWidth - 1 - len(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}

		fmt.Fprintf(os.Stderr, "\r%s", line)
		if i < n-1 {
			fmt.Fprint(os.Stderr, "\n")
		}
	}

	p.rendered = true
}

// formatDestLine builds the status line for a single destination.
// Caller must hold p.mu.
func (p *multiDestProgress) formatDestLine(d *destState) string {
	sep := cliStyles.Muted.Render(" · ")
	namePad := fmt.Sprintf("%-*s", p.maxNameLen, d.name)

	if d.done {
		icon := cliStyles.Success.Render("✓")
		if d.err != nil {
			icon = cliStyles.Error.Render("✗")
		}
		var parts []string
		s := d.stats
		parts = append(parts, fmt.Sprintf("%s read", formatBytes(s.BytesRead)))
		skipped := s.BytesRead - s.BytesNew
		if skipped > 0 {
			parts = append(parts, fmt.Sprintf("%s skipped", formatBytes(skipped)))
		}
		if d.err != nil {
			parts = append(parts, cliStyles.Error.Render("failed: "+d.err.Error()))
		} else {
			parts = append(parts, cliStyles.Success.Render(fmt.Sprintf("done (%s)", d.stats.Elapsed.Round(time.Millisecond))))
		}
		return fmt.Sprintf("  %s %s  %s", icon, namePad, strings.Join(parts, sep))
	}

	// In progress.
	spinner := cliStyles.Brand.Render(spinnerFrames[p.spinIdx])
	s := d.stats

	var parts []string

	if s.FilesProcessed == 0 {
		parts = append(parts, "scanning")
		if s.FilesTotal > 0 {
			parts = append(parts, fmt.Sprintf("%s files", formatCount(s.FilesTotal)))
		}
	} else {
		parts = append(parts, fmt.Sprintf("%s read", formatBytes(s.BytesRead)))
		skipped := s.BytesRead - s.BytesNew
		if skipped > 0 {
			parts = append(parts, fmt.Sprintf("%s skipped", formatBytes(skipped)))
		}

		// Speed.
		if s.Elapsed > 0 && s.BytesRead > 0 {
			speed := float64(s.BytesRead) / s.Elapsed.Seconds()
			parts = append(parts, formatBytes(int64(speed))+"/s")
		}

		// Current file.
		if s.CurrentFile != "" {
			// Truncate path to fit remaining space.
			maxFileLen := p.termWidth - len(namePad) - 30 - (len(parts) * 5)
			if maxFileLen < 10 {
				maxFileLen = 10
			}
			file := s.CurrentFile
			if len(file) > maxFileLen {
				file = "..." + file[len(file)-maxFileLen+3:]
			}
			parts = append(parts, cliStyles.Muted.Render(file))
		}
	}

	return fmt.Sprintf("  %s %s  %s", spinner, namePad, strings.Join(parts, sep))
}

// renderLog emits periodic log messages for non-interactive output (per destination).
func (p *multiDestProgress) renderLog(idx int, stats backup.Stats) {
	p.mu.Lock()
	d := p.dests[idx]
	now := time.Now()

	if d.lastLog.IsZero() {
		d.lastLog = now
		p.mu.Unlock()
		return
	}

	if now.Sub(d.lastLog) < 5*time.Second {
		p.mu.Unlock()
		return
	}
	d.lastLog = now
	name := d.name
	p.mu.Unlock()

	if stats.FilesProcessed == 0 {
		logger.Info("Scanning", "dest", name, "files", stats.FilesTotal, "dirs", stats.DirsTotal)
	} else {
		logger.Info("Progress",
			"dest", name,
			"files", fmt.Sprintf("%d/%d", stats.FilesProcessed, stats.FilesTotal),
			"read", formatBytes(stats.BytesRead),
			"new", formatBytes(stats.BytesNew),
			"dedup", stats.ChunksDup,
		)
	}
}

// formatCount formats an integer with comma separators.
func formatCount(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
