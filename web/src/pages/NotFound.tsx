import { Link } from 'react-router-dom'
import { Home, ArrowLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'

export default function NotFound() {
  return (
    <div className="min-h-screen bg-zinc-950 flex items-center justify-center p-6">
      <div className="w-full max-w-md text-center">
        {/* 404 number */}
        <div className="text-8xl font-bold text-zinc-800 select-none leading-none mb-2">
          404
        </div>

        {/* Message */}
        <h1 className="text-xl font-semibold text-white mb-2">Page Not Found</h1>
        <p className="text-zinc-400 text-sm mb-8">
          The page you are looking for does not exist or has been moved.
        </p>

        {/* Actions */}
        <div className="flex items-center justify-center gap-3">
          <Button render={<Link to="/" />} className="bg-blue-600 hover:bg-blue-500 text-white">
            <Home className="w-4 h-4 mr-2" />
            Go to Dashboard
          </Button>
          <Button
            variant="outline"
            onClick={() => window.history.back()}
            className="border-zinc-700 text-zinc-300 hover:bg-zinc-800 hover:text-white"
          >
            <ArrowLeft className="w-4 h-4 mr-2" />
            Go Back
          </Button>
        </div>
      </div>
    </div>
  )
}
