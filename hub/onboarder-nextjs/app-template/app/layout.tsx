import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  metadataBase: new URL('https://blaxel.ai'),
  title: 'Blaxel Sandbox Lab',
  description:
    'A live Blaxel sandbox starter with safe commands, agent handoff, preview proof, and platform docs links.',
  icons: {
    apple: '/logo_short.png',
    icon: '/logo_short.png',
    shortcut: '/logo_short.png',
  },
  openGraph: {
    description:
      'Give your agent a disposable computer: run commands, inspect files, serve previews, and return proof from a Blaxel sandbox.',
    images: ['/logo_white.png'],
    title: 'Blaxel Sandbox Lab',
    type: 'website',
  },
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
