import { NextResponse } from 'next/server';

export const runtime = 'nodejs';

export async function GET() {
  return NextResponse.json({
    app: 'blaxel-sandbox-lab',
    cwd: process.cwd(),
    ok: true,
    timestamp: new Date().toISOString(),
  });
}
