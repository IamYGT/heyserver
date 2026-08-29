import React, { Suspense } from 'react'
import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, getToken } from '@/lib/api'
import Layout from '@/components/Layout'
import ErrorBoundary from '@/components/ErrorBoundary'
import AuthGuard from '@/components/AuthGuard'
import LoadingSpinner from '@/components/LoadingSpinner'

interface OnboardingState {
  completed: boolean
  step: number
}

function OnboardingGuard({ children }: { children: React.ReactNode }) {
  const location = useLocation()
  const token = getToken()

  const { data, isLoading } = useQuery<OnboardingState>({
    queryKey: ['onboarding'],
    queryFn: () => api.get<OnboardingState>('/onboarding'),
    enabled: !!token,
    staleTime: 5 * 60 * 1000,
    retry: false,
  })

  if (isLoading) return <>{children}</>
  if (location.pathname === '/onboarding') return <>{children}</>
  if (data && !data.completed) {
    return <Navigate to="/onboarding" replace />
  }
  return <>{children}</>
}

export default function ProtectedLayout() {
  return (
    <AuthGuard>
      <OnboardingGuard>
        <Layout>
          <ErrorBoundary>
            <Suspense fallback={<LoadingSpinner />}>
              <Outlet />
            </Suspense>
          </ErrorBoundary>
        </Layout>
      </OnboardingGuard>
    </AuthGuard>
  )
}
