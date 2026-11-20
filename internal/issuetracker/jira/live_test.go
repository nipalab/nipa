package jira

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/issuetracker"
)

// TestLiveSite reads one issue from a real Jira. NIPA_JIRA_TEST_AUTH picks
// "bearer" (Data Center PAT) instead of "basic" (Cloud email + API token).
func TestLiveSite(t *testing.T) {
	base := os.Getenv("NIPA_JIRA_TEST_URL")
	account := os.Getenv("NIPA_JIRA_TEST_ACCOUNT")
	token := os.Getenv("NIPA_JIRA_TEST_TOKEN")
	key := os.Getenv("NIPA_JIRA_TEST_ISSUE")
	if base == "" || account == "" || token == "" || key == "" {
		t.Skip("NIPA_JIRA_TEST_URL, NIPA_JIRA_TEST_ACCOUNT, NIPA_JIRA_TEST_TOKEN and NIPA_JIRA_TEST_ISSUE are not set")
	}
	auth := os.Getenv("NIPA_JIRA_TEST_AUTH")
	if auth == "" {
		auth = issuetracker.AuthBasic
	}
	client, err := New(issuetracker.Config{BaseURL: base, Account: account, Secret: token, AuthKind: auth, Transport: http.DefaultTransport})
	require.NoError(t, err)
	ctx := context.Background()

	site, err := client.Probe(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, site.AccountID)
	t.Logf("site %q version %s deployment %s as %s", site.Title, site.Version, site.Deployment, site.AccountName)

	issue, err := client.Issue(ctx, key)
	require.NoError(t, err)
	require.Equal(t, key, issue.Key)
	require.NotEmpty(t, issue.Status)
	t.Logf("%s %q [%s/%s]", issue.Key, issue.Title, issue.Status, issue.StatusCategory)
}
