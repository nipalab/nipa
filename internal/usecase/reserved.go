package usecase

// reservedOrgSlugs are first-segment paths owned by the SPA router, the SaaS
// marketing site or fixed HTTP endpoints. An organization with one of these
// slugs would be shadowed by those routes.
var reservedOrgSlugs = map[string]struct{}{
	"admin":      {},
	"api":        {},
	"assets":     {},
	"auth":       {},
	"dashboard":  {},
	"debug":      {},
	"docs":       {},
	"healthz":    {},
	"login":      {},
	"metrics":    {},
	"oauth":      {},
	"onboarding": {},
	"readyz":     {},
	"register":   {},
	"settings":   {},
	"verify":     {},
	"version":    {},
	"web":        {},
}

// reservedProjectSlugs are second-segment route literals under /{org}/.
var reservedProjectSlugs = map[string]struct{}{
	"new":      {},
	"settings": {},
}

func isReservedOrgSlug(slug string) bool {
	_, ok := reservedOrgSlugs[slug]
	return ok
}

func isReservedProjectSlug(slug string) bool {
	_, ok := reservedProjectSlugs[slug]
	return ok
}
