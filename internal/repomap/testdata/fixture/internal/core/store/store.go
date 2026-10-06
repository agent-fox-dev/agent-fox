package store

type Store struct{}

type StoreOptions struct{}

func CloseFrame() {}

func WriteBlock() {}

func StoreTable() {}

func ScanTable() {}

func CloseIndex() {}

func SplitTable() {}

func SealRecord() {}

func (s *Store) ReadIndex() {}

func (s *Store) ProbeJournal() {}

func mergeCursor() {}

func flushJournal() {}

func splitCursor() {}

func scanFrame() {}

func mergeFrame() {}

func flushIndex() {}

const defaultStoreSize = 64
