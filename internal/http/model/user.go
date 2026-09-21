package model

type UserResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	PhotoUrl     string `json:"photo_url"`
	IsAdmin      bool   `json:"is_admin"`
	IsSuperAdmin bool   `json:"is_super_admin"`
	Deleted      bool   `json:"deleted"`
}

type CreateUserRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type UpdateUserRequest struct {
	Email string `json:"email"`
}

type UpdateUserFlagsRequest struct {
	IsAdmin      bool `json:"is_admin"`
	IsSuperAdmin bool `json:"is_super_admin"`
}

type ResetPasswordRequest struct {
	Password string `json:"password"`
}

type UpdateProfileRequest struct {
	Name     string `json:"name"`
	PhotoUrl string `json:"photo_url"`
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}
