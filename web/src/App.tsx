import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, RequireAuth } from './auth'
import AppShell from './components/AppShell'
import LoginPage from './pages/LoginPage'
import HomePage from './pages/HomePage'
import OrgProjectsPage from './pages/OrgProjectsPage'
import RepoPage from './pages/RepoPage'
import BlobPage from './pages/BlobPage'
import CommitsPage from './pages/CommitsPage'
import BranchesPage from './pages/BranchesPage'
import PullsPage from './pages/PullsPage'
import PullPage from './pages/PullPage'
import ProjectSettingsPage from './pages/ProjectSettingsPage'
import OrgSettingsPage from './pages/OrgSettingsPage'
import AdminUsersPage from './pages/AdminUsersPage'
import ProfilePage from './pages/ProfilePage'

export default function App() {
  return (
    <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route element={<RequireAuth />}>
            <Route element={<AppShell />}>
              <Route path="/" element={<HomePage />} />
              <Route path="/admin/users" element={<AdminUsersPage />} />
              <Route path="/settings/profile" element={<ProfilePage />} />
              <Route path="/:org" element={<OrgProjectsPage />} />
              <Route path="/:org/settings" element={<OrgSettingsPage />} />
              <Route path="/:org/:project" element={<RepoPage />} />
              <Route path="/:org/:project/tree" element={<RepoPage />} />
              <Route path="/:org/:project/tree/:rev/*" element={<RepoPage />} />
              <Route path="/:org/:project/blob" element={<BlobPage />} />
              <Route path="/:org/:project/blob/:rev/*" element={<BlobPage />} />
              <Route path="/:org/:project/commits" element={<CommitsPage />} />
              <Route path="/:org/:project/commits/:commit" element={<CommitsPage />} />
              <Route path="/:org/:project/branches" element={<BranchesPage />} />
              <Route path="/:org/:project/pulls" element={<PullsPage />} />
              <Route path="/:org/:project/pulls/:id" element={<PullPage />} />
              <Route path="/:org/:project/settings" element={<ProjectSettingsPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Route>
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  )
}
