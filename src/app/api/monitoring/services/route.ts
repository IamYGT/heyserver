import { NextResponse } from 'next/server';
import { MonitoringService } from '@/lib/services/monitoring';

export const dynamic = 'force-dynamic';

export async function GET() {
  try {
    const services = await MonitoringService.getServiceStatus();
    return NextResponse.json({ services, timestamp: Date.now() });
  } catch (error) {
    console.error('Failed to get service status:', error);
    return NextResponse.json(
      { error: 'Failed to retrieve service statuses' },
      { status: 500 }
    );
  }
}
