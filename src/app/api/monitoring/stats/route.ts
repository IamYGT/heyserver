import { NextResponse } from 'next/server';
import { MonitoringService } from '@/lib/services/monitoring';

export const dynamic = 'force-dynamic';

export async function GET() {
  try {
    const stats = await MonitoringService.getStats();
    return NextResponse.json(stats);
  } catch (error) {
    console.error('Failed to get monitoring stats:', error);
    return NextResponse.json(
      { error: 'Failed to retrieve system stats' },
      { status: 500 }
    );
  }
}
