// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// A recording's still frames as a strip of thumbnails (ARCHITECTURE.md "Recorder"): taken at the
// start, every 30 s and at stop for the summaries; a click plays the recording from there. Over
// http an <img> loads the daemon's URL with a playback ticket; through the desktop bridge (or
// when that fails) the frame comes through the transport. Thumbnails load as they come into view.
import { useEffect, useRef, useState } from "react";
import { Images } from "lucide-react";
import { frameURL, recApi } from "@/lib/recordings";
import type { Recording, RecordingFrame } from "@/lib/recording-types";
import { clock } from "@/lib/recording-types";
import { useStore } from "@/lib/store";

/** The segment and the time in it of ms of recorded time. */
function place(rec: Recording, ms: number): { n: number; ms: number } {
  let t = 0;
  for (let i = 0; i < rec.segments.length; i++) {
    const g = rec.segments[i];
    if (ms < t + g.duration_ms || i === rec.segments.length - 1) return { n: g.n, ms: Math.max(0, ms - t) };
    t += g.duration_ms;
  }
  return { n: 1, ms };
}

export function FrameStrip({ rec, onSeek }: { rec: Recording; onSeek: (n: number, ms: number) => void }) {
  const frames = rec.frames ?? [];
  const kind = useStore((s) => s.transport?.kind);
  // a ticket for the <img> URLs (http); null while it comes, false: through the transport
  const [ticket, setTicket] = useState<string | null | false>(null);
  const strip = useRef<HTMLDivElement>(null);
  const has = frames.length > 0;
  useEffect(() => {
    if (!has) return;
    if (kind !== "http") return setTicket(false);
    let gone = false;
    recApi
      .ticket(rec.id)
      .then((t) => !gone && setTicket(t.ticket))
      .catch(() => !gone && setTicket(false));
    return () => {
      gone = true;
    };
  }, [rec.id, kind, has]);
  if (!has) return null;
  return (
    <section aria-label="Frames" className="space-y-1.5">
      <h3 className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <Images className="size-3.5" />
        Frames
        <span className="font-normal text-subtle-foreground">· {frames.length}, for summaries and your agents</span>
      </h3>
      <div ref={strip} className="scroll-thin flex gap-1.5 overflow-x-auto pb-1" data-frame-strip>
        {frames.map((f) => (
          <Thumb
            key={f.ms}
            rec={rec}
            frame={f}
            ticket={ticket}
            root={strip}
            onSeek={() => {
              const p = place(rec, f.ms);
              onSeek(p.n, p.ms);
            }}
          />
        ))}
      </div>
    </section>
  );
}

function Thumb({
  rec,
  frame,
  ticket,
  root,
  onSeek,
}: {
  rec: Recording;
  frame: RecordingFrame;
  ticket: string | null | false;
  root: React.RefObject<HTMLDivElement | null>;
  onSeek: () => void;
}) {
  const el = useRef<HTMLButtonElement>(null);
  const [seen, setSeen] = useState(false);
  const [blob, setBlob] = useState(false); // through the transport
  const [src, setSrc] = useState("");
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const b = el.current;
    if (!b || seen) return;
    if (typeof IntersectionObserver === "undefined") return setSeen(true);
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && setSeen(true), { root: root.current, rootMargin: "0px 240px" });
    io.observe(b);
    return () => io.disconnect();
  }, [seen, root]);

  useEffect(() => {
    if (!seen) return;
    if (ticket === null && !blob) return; // the ticket is on its way
    if (ticket && !blob) return setSrc(frameURL(rec.id, frame.ms, ticket));
    let gone = false;
    let url = "";
    recApi
      .frameData(rec.id, frame.ms)
      .then((d) => {
        if (gone) return;
        const bin = atob(d.data);
        const bytes = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
        url = URL.createObjectURL(new Blob([bytes], { type: d.type }));
        setSrc(url);
      })
      .catch(() => !gone && setFailed(true));
    return () => {
      gone = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [seen, ticket, blob, rec.id, frame.ms]);

  return (
    <button
      ref={el}
      type="button"
      onClick={onSeek}
      title={`Play from ${clock(frame.ms)}`}
      className="relative h-16 w-28 shrink-0 overflow-hidden rounded-md border bg-muted outline-none transition-opacity hover:opacity-85 focus-visible:ring-2 focus-visible:ring-ring"
      data-frame={frame.ms}
    >
      {src && !failed && (
        <img
          src={src}
          alt={`The picture at ${clock(frame.ms)}`}
          className="size-full object-cover"
          onError={() => (blob ? setFailed(true) : (setBlob(true), setSrc("")))}
          draggable={false}
        />
      )}
      <span className="tabular absolute bottom-1 left-1 rounded bg-black/60 px-1 font-mono text-[10px] leading-4 text-white">{clock(frame.ms)}</span>
    </button>
  );
}
