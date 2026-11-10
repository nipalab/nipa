import { Suspense, lazy } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, RedirectIfAuthenticated, RequireAuth, useAuth } from './auth'
import { Loading } from './components/ui'
import { APP_HOME } from './routes'
import AppShell from './components/AppShell'
import LoginPage from './pages/LoginPage'

const RegisterPage = lazy(() => import('./pages/RegisterPage'))
const VerifyPage = lazy(() => import('./pages/VerifyPage'))
const OAuthCallbackPage = lazy(() => import('./pages/OAuthCallbackPage'))
const OnboardingPage = lazy(() => import('./pages/OnboardingPage'))
const HomePage = lazy(() => import('./pages/HomePage'))
const OrgProjectsPage = lazy(() => import('./pages/OrgProjectsPage'))
const NewProjectPage = lazy(() => import('./pages/NewProjectPage'))
const RepoPage = lazy(() => import('./pages/RepoPage'))
const BlobPage = lazy(() => import('./pages/BlobPage'))
const CommitsPage = lazy(() => import('./pages/CommitsPage'))
const BranchesPage = lazy(() => import('./pages/BranchesPage'))
const NewBranchPage = lazy(() => import('./pages/NewBranchPage'))
const ComparePage = lazy(() => import('./pages/ComparePage'))
const TagsPage = lazy(() => import('./pages/TagsPage'))
const MergeRequestsPage = lazy(() => import('./pages/MergeRequestsPage'))
const MergeRequestPage = lazy(() => import('./pages/MergeRequestPage'))
const NewMergeRequestPage = lazy(() => import('./pages/NewMergeRequestPage'))
const LocksPage = lazy(() => import('./pages/LocksPage'))
const ProjectSettingsPage = lazy(() => import('./pages/ProjectSettingsPage'))
const OrgSettingsPage = lazy(() => import('./pages/OrgSettingsPage'))
const AdminUsersPage = lazy(() => import('./pages/AdminUsersPage'))
const NewUserPage = lazy(() => import('./pages/NewUserPage'))
const ProfilePage = lazy(() => import('./pages/ProfilePage'))
const BillingPage = lazy(() => import('./pages/BillingPage'))

export default function App() {
  return (
    <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AuthProvider>
        <Suspense fallback={<Loading />}>
          <Routes>
            <Route path="/" element={<RootRedirect />} />
            <Route element={<RedirectIfAuthenticated />}>
              <Route path="/login" element={<LoginPage />} />
              <Route path="/register" element={<RegisterPage />} />
            </Route>
            <Route path="/verify" element={<VerifyPage />} />
            <Route path="/oauth/callback" element={<OAuthCallbackPage />} />
            <Route element={<RequireAuth />}>
              <Route element={<AppShell />}>
                <Route path="/dashboard" element={<HomePage />} />
                <Route path="/onboarding" element={<OnboardingPage />} />
                <Route path="/admin/users" element={<AdminUsersPage />} />
                <Route path="/admin/users/new" element={<NewUserPage />} />
                <Route path="/settings/profile" element={<ProfilePage />} />
                <Route path="/settings/billing" element={<BillingPage />} />
                <Route path="/:org" element={<OrgProjectsPage />} />
                <Route path="/:org/settings" element={<OrgSettingsPage />} />
                <Route path="/:org/new" element={<NewProjectPage />} />
                <Route path="/:org/:project" element={<RepoPage />} />
                <Route path="/:org/:project/tree" element={<RepoPage />} />
                <Route path="/:org/:project/tree/:rev/*" element={<RepoPage />} />
                <Route path="/:org/:project/blob" element={<BlobPage />} />
                <Route path="/:org/:project/blob/:rev/*" element={<BlobPage />} />
                <Route path="/:org/:project/commits" element={<CommitsPage />} />
                <Route path="/:org/:project/commits/:commit" element={<CommitsPage />} />
                <Route path="/:org/:project/branches" element={<BranchesPage />} />
                <Route path="/:org/:project/branches/new" element={<NewBranchPage />} />
                <Route path="/:org/:project/compare" element={<ComparePage />} />
                <Route path="/:org/:project/tags" element={<TagsPage />} />
                <Route path="/:org/:project/merges" element={<MergeRequestsPage />} />
                <Route path="/:org/:project/merges/new" element={<NewMergeRequestPage />} />
                <Route path="/:org/:project/merges/:id" element={<MergeRequestPage />} />
                <Route path="/:org/:project/locks" element={<LocksPage />} />
                <Route path="/:org/:project/settings" element={<ProjectSettingsPage />} />
                <Route path="*" element={<Navigate to={APP_HOME} replace />} />
              </Route>
            </Route>
          </Routes>
        </Suspense>
      </AuthProvider>
    </BrowserRouter>
  )
}

function RootRedirect() {
  const { status } = useAuth()
  if (status === 'loading') {
    return <Loading />
  }
  if (status === 'anonymous') {
    return <Navigate to="/login" state={{ from: APP_HOME }} replace />
  }
  return <Navigate to={APP_HOME} replace />
}
