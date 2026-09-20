package handler

import (
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

func (h *Handler) resolveOrg(appCtx http.AppContext) (*domain.Organization, error) {
	return h.useCase.Common().ResolveOrg(appCtx.Context(), appCtx.PathParameter("org"))
}

func parseID(raw, what string) (snow.ID, error) {
	id, err := snow.ParseBase36(raw)
	if err != nil {
		return 0, domain.NewErrorUser("invalid " + what + " id")
	}
	return id, nil
}

func toUserResponse(user *domain.User) model.UserResponse {
	return model.UserResponse{
		ID:           user.ID.Base36(),
		Name:         user.Name,
		Email:        user.Email,
		PhotoUrl:     user.PhotoUrl,
		IsAdmin:      user.IsAdmin,
		IsSuperAdmin: user.IsSuperAdmin,
		Deleted:      user.Deleted,
	}
}

func toOrgMemberResponse(member *domain.OrgMember) model.OrgMemberResponse {
	joinedAt := member.JoinedAt
	return model.OrgMemberResponse{
		UserID:       member.User.ID.Base36(),
		Name:         member.User.Name,
		Email:        member.User.Email,
		PhotoUrl:     member.User.PhotoUrl,
		IsAdmin:      member.User.IsAdmin,
		IsSuperAdmin: member.User.IsSuperAdmin,
		Role:         member.Role,
		JoinedAt:     &joinedAt,
	}
}

func toGroupResponse(group *domain.Group, memberIDs []snow.ID) model.GroupResponse {
	resp := model.GroupResponse{
		ID:          group.ID.Base36(),
		OrgID:       group.OrgID.Base36(),
		Name:        group.Name,
		Description: group.Description,
	}
	for _, id := range memberIDs {
		resp.MemberIDs = append(resp.MemberIDs, id.Base36())
	}
	return resp
}
