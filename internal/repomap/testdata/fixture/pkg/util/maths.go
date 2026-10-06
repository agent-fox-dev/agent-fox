package util

type Maths struct{}

type MathsOptions struct{}

func StoreJournal() {}

func WriteBlock() {}

func MergeIndex() {}

func WriteCursor() {}

func WriteFrame() {}

func CloseCursor() {}

func CloseJournal() {}

func (s *Maths) SealBatch() {}

func (s *Maths) OpenPage() {}

func writeTable() {}

func sealFrame() {}

func writeIndex() {}

func sealIndex() {}

func probeSegment() {}

func probeRecord() {}

const defaultMathsSize = 64
