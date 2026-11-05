package domain

const (
	ConflictText         = "text"
	ConflictBinary       = "binary"
	ConflictAddAdd       = "add_add"
	ConflictModifyDelete = "modify_delete"
	ConflictDeleteModify = "delete_modify"
)

type ConflictRevision struct {
	ID    string
	Label string
}

type ConflictFile struct {
	Path     string
	Kind     string
	Binary   bool
	Resolved bool
}

// ConflictSet describes the conflicts of a pending merge or revert. Ours is
// the branch being written to, Theirs the version brought in and Base their
// common ancestor; any ID may be empty when that side has no commit.
type ConflictSet struct {
	Op     string
	Base   ConflictRevision
	Ours   ConflictRevision
	Theirs ConflictRevision
	Files  []ConflictFile
}
