import { StrictMode, Suspense } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Toaster } from 'sonner'
import Login from '@/pages/Login'
import AuthGuard from '@/components/AuthGuard'
import LoadingSpinner from '@/components/LoadingSpinner'
import OnboardingPage from '@/pages/Onboarding'
import ProtectedLayout from '@/components/ProtectedLayout'
import './index.css'

import {
  NotFound,
  PROTECTED_ROUTE_ENTRIES,
  ROUTE_PATHS,
} from '@/routes'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 10_000,
    },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path={ROUTE_PATHS.login} element={<Login />} />
          {/* Onboarding wizard — auth required but no Layout wrapper */}
          <Route
            path={ROUTE_PATHS.onboarding}
            element={
              <AuthGuard>
                <OnboardingPage />
              </AuthGuard>
            }
          />
          <Route element={<ProtectedLayout />}>
            {PROTECTED_ROUTE_ENTRIES.map(({ path, Component }) => (
              <Route key={path} path={path} element={<Component />} />
            ))}
          </Route>
          <Route
            path={ROUTE_PATHS.notFound}
            element={
              <Suspense fallback={<LoadingSpinner />}>
                <NotFound />
              </Suspense>
            }
          />
        </Routes>
        <Toaster theme="dark" position="top-right" />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
