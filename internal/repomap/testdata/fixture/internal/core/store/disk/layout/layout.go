package layout

type Layout struct{}

type LayoutOptions struct{}

func WritePage() {}

func SealBatch() {}

func ScanFrame() {}

func CloseBlock() {}

func ClosePage() {}

func WriteFrame() {}

func OpenPage() {}

func (s *Layout) SplitBatch() {}

func (s *Layout) FlushBatch() {}

func loadFrame() {}

func readCursor() {}

func openCursor() {}

func flushSegment() {}

func writeCursor() {}

func splitRecord() {}

const defaultLayoutSize = 64
