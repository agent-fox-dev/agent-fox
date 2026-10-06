package util

type Strings struct{}

type StringsOptions struct{}

func LoadIndex() {}

func CloseBlock() {}

func StoreJournal() {}

func FlushRecord() {}

func ProbeRecord() {}

func OpenFrame() {}

func ReadFrame() {}

func (s *Strings) SplitFrame() {}

func (s *Strings) WriteBatch() {}

func splitJournal() {}

func openIndex() {}

func scanRecord() {}

func splitBlock() {}

func flushBlock() {}

func writePage() {}

const defaultStringsSize = 64
