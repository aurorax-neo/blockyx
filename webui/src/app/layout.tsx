import { Geist, Jersey_15 } from "next/font/google";
import { Providers } from "~/app/providers";
import { cn } from "~/lib/utils";

import "../styles/globals.css";

export const metadata = {
  title: "BlockyUI",
  description: "A modern UI for Blocky DNS",
};

const geist = Geist({
  subsets: ["latin"],
  display: "swap",
  variable: "--font-geist",
});

const jersey15 = Jersey_15({
  subsets: ["latin"],
  weight: "400",
  display: "swap",
  variable: "--font-jersey15",
});

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html
      className={cn(geist.variable, jersey15.variable, "dark", "font-sans")}
      lang="en"
    >
      <body>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
