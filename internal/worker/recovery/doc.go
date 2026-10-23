// Package recovery is the record an attempt keeps beside its clone so that a later delivery of the same
// attempt can carry it on: the journal of what the attempt has done, and the outcome file that holds
// what it came to. Both live in recovery/ of the attempt directory, are written atomically and
// synced, and hold no credential.
package recovery
