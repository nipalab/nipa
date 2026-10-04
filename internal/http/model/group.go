package model

type GroupResponse struct {
	ID          string                `json:"id"`
	OrgID       string                `json:"org_id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	MemberIDs   []string              `json:"member_ids,omitempty"`
	Members     []GroupMemberResponse `json:"members,omitempty"`
	MemberCount int64                 `json:"member_count"`
}

type GroupMemberResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

type CreateGroupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type GroupMemberRequest struct {
	UserID string `json:"user_id"`
}
