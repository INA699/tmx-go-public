// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Still frames for the summaries (ARCHITECTURE.md "Recorder"): a JPEG of the screen or camera at the
// start, every 30 s of recorded time and at stop, at most 1280 px on the longer side and
// 600 KB. ImageCapture.grabFrame (Chromium, WebView2) reads the track itself, so it works while
// this window is hidden behind what is being recorded; elsewhere a tiny muted <video> plays the
// track and is drawn onto a canvas.

export const FRAME_EVERY_MS = 30_000;
/** the daemon keeps at most this many per recording */
export const MAX_FRAMES = 400;
const MAX_SIDE = 1280;
const MAX_BYTES = 600 << 10;
const FRAME_KEY = "tmx.rec.frame-every-ms";

/** The time between frames: 30 s of recorded time (a test sets localStorage "tmx.rec.frame-every-ms", at least 1 s). */
export function frameEvery(): number {
  try {
    const v = Number(localStorage.getItem(FRAME_KEY));
    if (Number.isFinite(v) && v >= 1000) return v;
  } catch {
    /* no storage here */
  }
  return FRAME_EVERY_MS;
}

type ImageCaptureLike = { grabFrame(): Promise<ImageBitmap> };
type ImageCaptureCtor = new (t: MediaStreamTrack) => ImageCaptureLike;

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise((res, rej) => {
    const id = setTimeout(() => rej(new Error("timed out")), ms);
    p.then(
      (v) => (clearTimeout(id), res(v)),
      (e) => (clearTimeout(id), rej(e)),
    );
  });
}

export class FrameGrabber {
  private track: MediaStreamTrack;
  private capture: ImageCaptureLike | null = null;
  private video: HTMLVideoElement | null = null;
  private canvas = document.createElement("canvas");

  constructor(track: MediaStreamTrack) {
    this.track = track;
    const IC = (window as unknown as { ImageCapture?: ImageCaptureCtor }).ImageCapture;
    if (IC) {
      try {
        this.capture = new IC(track);
      } catch {
        this.capture = null;
      }
    }
  }

  /** A JPEG of the track's picture now, or null when there is none (an ended track, no frame yet). */
  async grab(): Promise<Blob | null> {
    if (this.track.readyState !== "live") return null;
    if (this.capture) {
      try {
        const bm = await withTimeout(this.capture.grabFrame(), 3000);
        try {
          return await this.encode(bm, bm.width, bm.height);
        } finally {
          bm.close();
        }
      } catch {
        /* this time through the video element */
      }
    }
    const v = await this.videoElement();
    if (!v?.videoWidth) return null;
    return this.encode(v, v.videoWidth, v.videoHeight);
  }

  stop() {
    if (this.video) {
      this.video.srcObject = null;
      this.video.remove();
      this.video = null;
    }
  }

  private async videoElement(): Promise<HTMLVideoElement | null> {
    if (!this.video) {
      const v = document.createElement("video");
      v.muted = true;
      v.playsInline = true;
      v.setAttribute("aria-hidden", "true");
      // in the page (some engines draw no frames for a detached element), but not seen
      v.style.cssText = "position:fixed;right:0;bottom:0;width:2px;height:2px;opacity:0.01;pointer-events:none;z-index:-1";
      v.srcObject = new MediaStream([this.track]);
      document.body.appendChild(v);
      this.video = v;
      void v.play().catch(() => {});
    }
    const v = this.video;
    if (v.readyState < 2) {
      await withTimeout(
        new Promise<void>((res) => v.addEventListener("loadeddata", () => res(), { once: true })),
        3000,
      ).catch(() => {});
    }
    return v.readyState >= 2 ? v : null;
  }

  /** Draws src at most 1280 px on its longer side and encodes it as a JPEG of at most 600 KB. */
  private async encode(src: CanvasImageSource, w: number, h: number): Promise<Blob | null> {
    if (!w || !h) return null;
    let scale = Math.min(1, MAX_SIDE / Math.max(w, h));
    let quality = 0.7;
    for (let i = 0; i < 5; i++) {
      const cw = Math.max(1, Math.round(w * scale));
      const ch = Math.max(1, Math.round(h * scale));
      this.canvas.width = cw;
      this.canvas.height = ch;
      const g = this.canvas.getContext("2d");
      if (!g) return null;
      g.drawImage(src, 0, 0, cw, ch);
      const blob = await new Promise<Blob | null>((res) => this.canvas.toBlob(res, "image/jpeg", quality));
      if (!blob) return null;
      if (blob.size <= MAX_BYTES) return blob;
      if (quality > 0.45) quality -= 0.15;
      else scale *= 0.75;
    }
    return null;
  }
}
