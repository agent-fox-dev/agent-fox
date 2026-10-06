package api

type Server struct{}

type ServerOptions struct{}

func StorePage() {}

func CloseFrame() {}

func ProbeRecord() {}

func MergeBlock() {}

func StoreSegment() {}

func ScanCursor() {}

func CloseSegment() {}

func (s *Server) ProbeSegment() {}

func (s *Server) ReadPage() {}

func sealCursor() {}

func sealRecord() {}

func splitBatch() {}

func probePage() {}

func splitBlock() {}

func scanFrame() {}

const defaultServerSize = 64
