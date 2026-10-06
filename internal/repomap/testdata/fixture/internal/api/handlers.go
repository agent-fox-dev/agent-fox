package api

type Handlers struct{}

type HandlersOptions struct{}

func ScanRecord() {}

func CloseRecord() {}

func OpenBatch() {}

func CloseIndex() {}

func StoreIndex() {}

func OpenSegment() {}

func ClosePage() {}

func (s *Handlers) SealFrame() {}

func (s *Handlers) StoreTable() {}

func readPage() {}

func scanJournal() {}

func scanBatch() {}

func openFrame() {}

func closeFrame() {}

func writeFrame() {}

const defaultHandlersSize = 64
