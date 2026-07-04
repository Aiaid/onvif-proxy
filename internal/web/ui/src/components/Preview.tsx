import { useEffect, useRef } from "preact/hooks";
import { useT } from "../i18n";

interface Props {
  uuid: string;
  stream: string;
  onClose: () => void;
}

// Preview shows the live MJPEG stream in a full-screen overlay. Clearing the
// <img> src on unmount closes the multipart connection, which the server treats
// as the client disconnecting and kills the backing ffmpeg process.
export function Preview({ uuid, stream, onClose }: Props) {
  const t = useT();
  const imgRef = useRef<HTMLImageElement>(null);
  const src =
    "/api/preview?device=" +
    encodeURIComponent(uuid) +
    "&stream=" +
    encodeURIComponent(stream);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    const img = imgRef.current;
    return () => {
      window.removeEventListener("keydown", onKey);
      if (img) img.src = ""; // drop the stream connection
    };
  }, [onClose]);

  return (
    <div class="overlay" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <img ref={imgRef} src={src} alt={t.previewAlt} />
      <button type="button" class="primary" onClick={onClose}>
        {t.previewClose}
      </button>
    </div>
  );
}
