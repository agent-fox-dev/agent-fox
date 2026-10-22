// Package jobs is the jobs layer of the agent-fox service: the durable record of
// a triage or fix request against a subscribed hub workspace (a job), the attempts
// that carry it out, and the log of events a client can follow.
//
// A job is an af_jobs row, an attempt an af_attempts row keyed by the job id and
// the attempt number, and the log of a job an af_events table keyed by the job id
// and a sequence number without a gap. The Store keeps them and builds the
// documents that every route serves. What an attempt does is the business of a
// Runner, behind the seam in runner.go; this package ships a placeholder runner
// and, in the package fakerunner, a runner that tests script.
//
// The layer sits on aflib/jobqueue, which keeps the queue durable, hands items
// over, retries them after a delay and recovers the ones that were claimed when
// the process died. See queue.go for what the layer adds to it and why.
package jobs
