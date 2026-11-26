import { useEffect, useRef, useState } from "react";
import { LuCamera, LuCircle, LuDownload, LuSquare, LuTrash2 } from "react-icons/lu";

import { Button } from "@components/Button";
import {
  SplitButtonCaret,
  SplitButtonGroup,
  SplitButtonPrimary,
  type SplitButtonMenuItem,
} from "@components/SplitButton";
import { useRTCStore, useVideoStore } from "@hooks/stores";
import {
  deleteRecording,
  downloadRecording,
  recordingType,
  refreshRecordings,
  startRecording,
  stopRecording,
  takeScreenshot,
  useCaptureStore,
} from "@hooks/useStreamCapture";
import notifications from "@/notifications";
import { formatters } from "@/utils";
import { m } from "@localizations/messages.js";

function elapsed(ms: number) {
  const s = Math.floor(ms / 1000);
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

// One split button between Settings and Fullscreen: Screenshot, and in its
// menu Record and the stored recordings. While recording, a Stop button
// with the time and size shows beside it.
export default function CaptureButtons() {
  const video = useVideoStore(state => state.videoElement);
  const stream = useRTCStore(state => state.mediaStream);
  const { recording, startedAt, bytes, recordings } = useCaptureStore();
  const [now, setNow] = useState(Date.now());
  const canRecord = recordingType() !== null;

  // The stored recordings are listed on first hover or focus, not on every page load.
  const scanned = useRef(false);
  const scanOnce = () => {
    if (scanned.current) return;
    scanned.current = true;
    void refreshRecordings();
  };

  useEffect(() => {
    if (!recording) return;
    const timer = window.setInterval(() => setNow(Date.now()), 500);
    return () => window.clearInterval(timer);
  }, [recording]);

  const fail = (error: unknown) =>
    notifications.error(error instanceof Error ? error.message : String(error));

  const menu: SplitButtonMenuItem[] = [
    ...(canRecord
      ? [
          recording
            ? {
                label: m.capture_stop({
                  time: elapsed(now - startedAt),
                  size: formatters.bytes(bytes, 1),
                }),
                icon: LuSquare,
                onClick: () => void stopRecording().catch(fail),
              }
            : {
                label: m.capture_record(),
                icon: LuCircle,
                onClick: () => void startRecording(stream).catch(fail),
              },
        ]
      : []),
    ...recordings.slice(0, 8).flatMap(rec => [
      {
        label: `${rec.name} · ${formatters.bytes(rec.size, 1)}`,
        icon: LuDownload,
        onClick: () => void downloadRecording(rec.name).catch(fail),
      },
      {
        label: m.capture_delete({ name: rec.name }),
        icon: LuTrash2,
        onClick: () => void deleteRecording(rec.name).catch(fail),
      },
    ]),
  ];

  return (
    <div className="flex items-center gap-x-2" onPointerEnter={scanOnce} onFocus={scanOnce}>
      <SplitButtonGroup>
        <SplitButtonPrimary
          icon={LuCamera}
          label={m.capture_screenshot()}
          onClick={() => void takeScreenshot(video).catch(fail)}
        />
        {menu.length > 0 && <SplitButtonCaret menuItems={menu} />}
      </SplitButtonGroup>
      {recording && (
        <Button
          size="XS"
          theme="danger"
          text={m.capture_stop({
            time: elapsed(now - startedAt),
            size: formatters.bytes(bytes, 1),
          })}
          LeadingIcon={LuSquare}
          onClick={() => void stopRecording().catch(fail)}
        />
      )}
    </div>
  );
}
