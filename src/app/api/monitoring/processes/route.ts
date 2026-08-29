import { NextResponse } from 'next/server';
import { MonitoringService } from '@/lib/services/monitoring';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  try {
    const { searchParams } = new URL(request.url);
    const limit = parseInt(searchParams.get('limit') ?? '20', 10);

    const processes = await MonitoringService.getTopProcesses(Math.min(limit, 50));
    return NextResponse.json({ processes, timestamp: Date.now() });
  } catch (error) {
    console.error('Failed to get processes:', error);
    return NextResponse.json(
      { error: 'Failed to retrieve process list' },
      { status: 500 }
    );
  }
}
