import { type ReactNode, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { getToken } from '@/lib/api'

interface AuthGuardProps {
  children: ReactNode
}

/**
 * Wraps protected routes.
 * - If no token is found in localStorage, redirects to /login immediately.
 * - The api.ts request function already handles 401 responses by clearing the
 *   token and redirecting, so we only need to guard the initial render here.
 */
export function AuthGuard({ children }: AuthGuardProps) {
  const navigate = useNavigate()
  const token = getToken()

  useEffect(() => {
    if (!token) {
      navigate('/login', { replace: true })
    }
  }, [token, navigate])

  if (!token) {
    return null
  }

  return <>{children}</>
}

export default AuthGuard
