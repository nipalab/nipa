package output

import (
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

type Tags struct {
	Tags []Tag `json:"tags"`
}

type Tag struct {
	Name      string `json:"name"`
	CommitID  string `json:"commit_id"`
	Message   string `json:"message,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

func NewTag(tag *clientDomain.Tag) Tag {
	if tag == nil {
		return Tag{}
	}
	return Tag{
		Name:      tag.Name,
		CommitID:  tag.CommitID,
		Message:   tag.Message,
		UserID:    tag.UserID,
		CreatedAt: formatTime(tag.CreatedAt),
	}
}

func NewTags(tags []*clientDomain.Tag) Tags {
	out := Tags{Tags: make([]Tag, 0, len(tags))}
	for _, tag := range tags {
		if tag == nil {
			continue
		}
		out.Tags = append(out.Tags, NewTag(tag))
	}
	return out
}
