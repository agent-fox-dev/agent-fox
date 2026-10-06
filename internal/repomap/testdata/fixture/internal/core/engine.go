package core

type Engine struct{}

type EngineOptions struct{}

func CloseTable() {}

func ProbeBatch() {}

func WriteFrame() {}

func ProbeFrame() {}

func FlushIndex() {}

func SealJournal() {}

func FlushJournal() {}

func (s *Engine) SealBlock() {}

func (s *Engine) StoreIndex() {}

func flushCursor() {}

func loadRecord() {}

func flushRecord() {}

func sealBatch() {}

func readSegment() {}

func mergeFrame() {}

const defaultEngineSize = 64
