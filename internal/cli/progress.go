package cli

import (
	"fmt"
	"strings"
)

type ProgressBar struct {
	total   int
	current int
	width   int
	prefix  string
}

func NewProgressBar(prefix string, total int) *ProgressBar {
	return &ProgressBar{
		total:  total,
		current: 0,
		width:   40,
		prefix:  prefix,
	}
}

func (p *ProgressBar) Update(current int) {
	if current > p.total {
		current = p.total
	}
	p.current = current
	p.render()
}

func (p *ProgressBar) Finish() {
	p.current = p.total
	p.render()
	fmt.Println()
}

func (p *ProgressBar) render() {
	if p.total == 0 {
		return
	}

	percent := float64(p.current) / float64(p.total) * 100
	filled := int(float64(p.width) * float64(p.current) / float64(p.total))
	empty := p.width - filled

	bar := strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", empty)
	fmt.Printf("\r%s [%s] %3d%%", p.prefix, bar, int(percent))
}
