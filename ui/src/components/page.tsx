// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/** Top bar of a page. Doubles as the window's drag region in the desktop app. */
export function PageHeader({ children, actions, className }: { children: ReactNode; actions?: ReactNode; className?: string }) {
  return (
    <header className={cn("drag-region flex h-[52px] shrink-0 items-center gap-3 border-b px-5 md:px-6", className)}>
      <div className="flex min-w-0 flex-1 items-center gap-2">{children}</div>
      {actions && <div className="flex shrink-0 items-center gap-1.5">{actions}</div>}
    </header>
  );
}

export function PageTitle({ children, className }: { children: ReactNode; className?: string }) {
  return <h1 className={cn("truncate text-[14px] font-semibold tracking-tight", className)}>{children}</h1>;
}

export function PageBody({ children, className, width = "max-w-[1040px]" }: { children: ReactNode; className?: string; width?: string }) {
  return (
    // relative: anything absolutely positioned inside (sr-only text, popper anchors) stays in
    // this scroller instead of stretching the whole window, which then scrolled with it.
    <div className="scroll-thin relative min-h-0 flex-1 overflow-y-auto">
      <div className={cn("mx-auto w-full px-5 py-6 md:px-8 md:py-8", width, className)}>{children}</div>
    </div>
  );
}

export function Section({
  title,
  right,
  children,
  className,
}: {
  title: ReactNode;
  right?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={className}>
      <div className="mb-2.5 flex h-7 items-center gap-2">
        <h2 className="text-[13px] font-medium text-foreground">{title}</h2>
        {right && <div className="ml-auto flex items-center gap-1.5">{right}</div>}
      </div>
      {children}
    </section>
  );
}

export function EmptyState({
  icon: Icon,
  title,
  children,
  actions,
  className,
}: {
  icon: React.ComponentType<{ className?: string }>;
  title: string;
  children?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center rounded-xl border bg-card px-6 py-12 text-center", className)}>
      <div className="mb-4 flex size-10 items-center justify-center rounded-lg border bg-muted/50">
        <Icon className="size-[18px] text-muted-foreground" />
      </div>
      <p className="text-[14px] font-medium">{title}</p>
      {children && <div className="mt-1.5 max-w-sm text-sm text-muted-foreground">{children}</div>}
      {actions && <div className="mt-5 flex flex-wrap items-center justify-center gap-2">{actions}</div>}
    </div>
  );
}
