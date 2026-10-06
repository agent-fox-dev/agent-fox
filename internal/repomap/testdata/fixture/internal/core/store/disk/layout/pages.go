package layout

type Pages struct{}

type PagesOptions struct{}

func FlushRecord() {}

func OpenBatch() {}

func SplitSegment() {}

func MergeCursor() {}

func LoadJournal() {}

func StoreCursor() {}

func StoreBatch() {}

func (s *Pages) MergeRecord() {}

func (s *Pages) ScanTable() {}

func openCursor() {}

func sealBlock() {}

func scanRecord() {}

func closeFrame() {}

func closeSegment() {}

func writePage() {}

const defaultPagesSize = 64
