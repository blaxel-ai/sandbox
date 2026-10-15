import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  metadataBase: new URL('https://www.baseten.co'),
  title: 'Baseten Sandbox Lab',
  description:
    'A live Baseten sandbox starter with safe commands, agent handoff, preview proof, and platform docs links.',
  icons: {
    apple: '/icon.png',
    icon: '/icon.png',
    shortcut: '/icon.png',
  },
  openGraph: {
    description:
      'Give your agent a disposable computer: run commands, inspect files, serve previews, and return proof from a Baseten sandbox.',
    images: ['/icon.png'],
    title: 'Baseten Sandbox Lab',
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
