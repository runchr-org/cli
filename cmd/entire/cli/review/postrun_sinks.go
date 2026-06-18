package review

import (
	"bytes"
	"io"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

type tuiPostRunCompleteSink struct {
	tui *TUISink
	buf *bytes.Buffer
	out io.Writer
}

func (s tuiPostRunCompleteSink) AgentEvent(_ string, _ reviewtypes.Event) {}

func (s tuiPostRunCompleteSink) RunFinished(_ reviewtypes.RunSummary) {
	if s.tui != nil {
		s.tui.PostRunComplete()
	}
	s.flushBuffer()
}

func (s tuiPostRunCompleteSink) flushBuffer() {
	if s.buf == nil || s.out == nil || s.buf.Len() == 0 {
		return
	}
	if _, err := s.out.Write(s.buf.Bytes()); err != nil {
		return
	}
}
