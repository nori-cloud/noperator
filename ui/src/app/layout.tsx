import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "noperator",
  description: "Argo CD tenant overview",
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
