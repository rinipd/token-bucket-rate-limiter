import type { ReactNode } from "react";
import "./globals.css";

export const metadata = {
  title: "Rate Limiter Dashboard",
  description: "Live per-client rate-limiter stats",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-gray-950 text-gray-100">{children}</body>
    </html>
  );
}
