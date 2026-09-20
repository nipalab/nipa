package model

type GroupResponse struct {
	ID          string   `json:"id"`
	OrgID       string   `json:"org_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MemberIDs   []string `json:"member_ids,omitempty"`
}

type CreateGroupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type GroupMemberRequest struct {
	UserID string `json:"user_id"`
}
