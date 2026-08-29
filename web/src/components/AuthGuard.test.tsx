import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { getToken } from '@/lib/api'
import { AuthGuard } from './AuthGuard'

const navigateMock = vi.fn()

vi.mock('react-router-dom', () => ({
  useNavigate: () => navigateMock,
}))

vi.mock('@/lib/api', () => ({
  getToken: vi.fn(),
}))

describe('AuthGuard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('redirects to login when no token is present', () => {
    vi.mocked(getToken).mockReturnValue(null)

    const { container } = render(
      <AuthGuard>
        <div>Protected content</div>
      </AuthGuard>,
    )

    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
    expect(container.firstChild).toBeNull()
  })

  it('renders children when a token is present', () => {
    vi.mocked(getToken).mockReturnValue('valid-token')

    const { getByText } = render(
      <AuthGuard>
        <div>Protected content</div>
      </AuthGuard>,
    )

    expect(getByText('Protected content')).toBeInTheDocument()
    expect(navigateMock).not.toHaveBeenCalled()
  })
})
