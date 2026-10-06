package disk

type Disk struct{}

type DiskOptions struct{}

func StoreJournal() {}

func SealSegment() {}

func MergeBlock() {}

func ScanFrame() {}

func ScanJournal() {}

func StoreBlock() {}

func SplitIndex() {}

func (s *Disk) FlushJournal() {}

func (s *Disk) StoreCursor() {}

func scanTable() {}

func scanIndex() {}

func splitSegment() {}

func splitJournal() {}

func openBlock() {}

func storePage() {}

const defaultDiskSize = 64
