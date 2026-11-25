import { useInterval } from "usehooks-ts";
import { LuCopy } from "react-icons/lu";
import { useState } from "react";

import { m } from "@localizations/messages.js";
import { useRTCStore, useUiStore } from "@hooks/stores";
import { createChartArray, Metric } from "@components/Metric";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";
import SidebarHeader from "@components/SidebarHeader";
import { someIterable } from "@/utils";
import { GridCard } from "@components/Card";
import { Button } from "@components/Button";
import { useCopyToClipboard } from "@components/useCopyToClipBoard";
import notifications from "@/notifications";
import { useCameraWatch, useVirtualCamera } from "@hooks/VirtualCameraProvider";
import { useMediaDeviceStats } from "@hooks/useMediaDeviceStats";

function StatRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 text-sm">
      <div className="text-slate-600 dark:text-slate-400">{label}</div>
      <div className="min-w-0 text-right font-mono text-xs break-words text-black dark:text-white">
        {value}
      </div>
    </div>
  );
}

export default function ConnectionStatsSidebar() {
  const { sidebarView, setSidebarView } = useUiStore();
  const {
    mediaStream,
    peerConnection,
    inboundRtpStats: inboundVideoRtpStats,
    appendInboundRtpStats: appendInboundVideoRtpStats,
    candidatePairStats: iceCandidatePairStats,
    appendCandidatePairStats,
    appendLocalCandidateStats,
    appendRemoteCandidateStats,
    appendDiskDataChannelStats,
  } = useRTCStore();

  const camera = useVirtualCamera();
  useCameraWatch();
  const { cameraActive, microphoneActive, cameraHistory, microphoneHistory, microphoneState } =
    useMediaDeviceStats();
  const cameraState = camera.state;
  const cameraStats = camera.stats;

  const [remoteIPAddress, setRemoteIPAddress] = useState<string | null>(null);

  useInterval(function collectWebRTCStats() {
    (async () => {
      if (!mediaStream) return;

      const videoTrack = mediaStream.getVideoTracks()[0];
      if (!videoTrack) return;

      const stats = await peerConnection?.getStats();
      let successfulLocalCandidateId: string | null = null;
      let successfulRemoteCandidateId: string | null = null;

      stats?.forEach(report => {
        if (report.type === "inbound-rtp" && report.kind === "video") {
          appendInboundVideoRtpStats(report);
        } else if (report.type === "candidate-pair" && report.nominated) {
          if (report.state === "succeeded") {
            successfulLocalCandidateId = report.localCandidateId;
            successfulRemoteCandidateId = report.remoteCandidateId;
          }
          appendCandidatePairStats(report);
        } else if (report.type === "local-candidate") {
          // We only want to append the local candidate stats that were used in nominated candidate pair
          if (successfulLocalCandidateId === report.id) {
            appendLocalCandidateStats(report);
          }
        } else if (report.type === "remote-candidate") {
          if (successfulRemoteCandidateId === report.id) {
            appendRemoteCandidateStats(report);
            setRemoteIPAddress(report.address);
          }
        } else if (report.type === "data-channel" && report.label === "disk") {
          appendDiskDataChannelStats(report);
        }
      });
    })();
  }, 500);

  const jitterBufferDelay = createChartArray(inboundVideoRtpStats, "jitterBufferDelay");
  const jitterBufferEmittedCount = createChartArray(
    inboundVideoRtpStats,
    "jitterBufferEmittedCount",
  );

  const jitterBufferAvgDelayData = jitterBufferDelay.map((d, idx) => {
    if (idx === 0) return { date: d.date, metric: null };
    const prevDelay = jitterBufferDelay[idx - 1]?.metric as number | null | undefined;
    const currDelay = d.metric as number | null | undefined;
    const prevCountEmitted =
      (jitterBufferEmittedCount[idx - 1]?.metric as number | null | undefined) ?? null;
    const currCountEmitted =
      (jitterBufferEmittedCount[idx]?.metric as number | null | undefined) ?? null;

    if (
      prevDelay == null ||
      currDelay == null ||
      prevCountEmitted == null ||
      currCountEmitted == null
    ) {
      return { date: d.date, metric: null };
    }

    const deltaDelay = currDelay - prevDelay;
    const deltaEmitted = currCountEmitted - prevCountEmitted;

    // Guard counter resets or no emitted frames
    if (deltaDelay < 0 || deltaEmitted <= 0) {
      return { date: d.date, metric: null };
    }

    const valueMs = Math.round((deltaDelay / deltaEmitted) * 1000);
    return { date: d.date, metric: valueMs };
  });

  const { copy } = useCopyToClipboard();

  return (
    <div className="grid h-full grid-rows-(--grid-headerBody) shadow-xs">
      <SidebarHeader title={m.connection_stats_sidebar()} setSidebarView={setSidebarView} />
      <div className="h-full space-y-4 overflow-y-scroll bg-white px-4 py-2 pb-8 dark:bg-slate-900">
        <div className="space-y-4">
          {sidebarView === "connection-stats" && (
            <div className="space-y-8">
              {/* Connection Group */}
              <div className="space-y-3">
                <SettingsSectionHeader
                  title={m.connection_stats_connection()}
                  description={m.connection_stats_connection_description()}
                />
                {remoteIPAddress && (
                  <div className="flex items-center justify-between">
                    <div className="text-sm text-slate-600 dark:text-slate-400">
                      {m.connection_stats_remote_ip_address()}
                    </div>
                    <div className="flex items-center">
                      <GridCard cardClassName="rounded-r-none">
                        <div className="flex h-[34px] items-center px-3 font-mono text-xs text-black select-all dark:text-white">
                          {remoteIPAddress}
                        </div>
                      </GridCard>
                      <Button
                        className="rounded-l-none border-l-slate-800/30 dark:border-slate-300/20"
                        size="SM"
                        type="button"
                        theme="light"
                        LeadingIcon={LuCopy}
                        onClick={async () => {
                          if (await copy(remoteIPAddress)) {
                            notifications.success(
                              m.connection_stats_remote_ip_address_copy_success({
                                ip: remoteIPAddress,
                              }),
                            );
                          } else {
                            notifications.error(m.connection_stats_remote_ip_address_copy_error());
                          }
                        }}
                      />
                    </div>
                  </div>
                )}
                <Metric
                  title={m.connection_stats_round_trip_time()}
                  description={m.connection_stats_round_trip_time_description()}
                  stream={iceCandidatePairStats}
                  metric="currentRoundTripTime"
                  map={x => ({
                    date: x.date,
                    metric: x.metric != null ? Math.round(x.metric * 1000) : null,
                  })}
                  domain={[0, 600]}
                  unit={m.connection_stats_unit_milliseconds()}
                />
              </div>

              {/* Video Group */}
              <div className="space-y-3">
                <SettingsSectionHeader
                  title={m.connection_stats_video()}
                  description={m.connection_stats_video_description()}
                />

                {/* RTP Jitter */}
                <Metric
                  title={m.connection_stats_network_stability()}
                  badge={m.connection_stats_badge_jitter()}
                  badgeTheme="light"
                  description={m.connection_stats_network_stability_description()}
                  stream={inboundVideoRtpStats}
                  metric="jitter"
                  map={x => ({
                    date: x.date,
                    metric: x.metric != null ? Math.round(x.metric * 1000) : null,
                  })}
                  domain={[0, 10]}
                  unit={m.connection_stats_unit_milliseconds()}
                />

                {/* Playback Delay */}
                <Metric
                  title={m.connection_stats_playback_delay()}
                  description={m.connection_stats_playback_delay_description()}
                  badge={m.connection_stats_badge_jitter_buffer_avg_delay()}
                  badgeTheme="light"
                  data={jitterBufferAvgDelayData}
                  gate={inboundVideoRtpStats}
                  supported={
                    someIterable(inboundVideoRtpStats, ([, x]) => x.jitterBufferDelay != null) &&
                    someIterable(
                      inboundVideoRtpStats,
                      ([, x]) => x.jitterBufferEmittedCount != null,
                    )
                  }
                  domain={[0, 30]}
                  unit={m.connection_stats_unit_milliseconds()}
                />

                {/* Packets Lost */}
                <Metric
                  title={m.connection_stats_packets_lost()}
                  description={m.connection_stats_packets_lost_description()}
                  stream={inboundVideoRtpStats}
                  metric="packetsLost"
                  domain={[0, 100]}
                  unit={m.connection_stats_unit_packets()}
                />

                {/* Frames Per Second */}
                <Metric
                  title={m.connection_stats_frames_per_second()}
                  description={m.connection_stats_frames_per_second_description()}
                  stream={inboundVideoRtpStats}
                  metric="framesPerSecond"
                  domain={[0, 80]}
                  unit={m.connection_stats_unit_frames_per_second()}
                />
              </div>

              {/* USB camera */}
              {cameraActive && (
                <div className="space-y-3">
                  <SettingsSectionHeader
                    title={m.connection_stats_usb_camera()}
                    description={m.connection_stats_usb_camera_description()}
                  />
                  <Metric
                    title={m.connection_stats_camera_sent_fps()}
                    description={m.connection_stats_camera_sent_fps_description()}
                    stream={cameraHistory}
                    metric="sentFps"
                    map={x => ({
                      date: x.date,
                      metric: x.metric != null ? Math.round(x.metric) : null,
                    })}
                    domain={[0, 60]}
                    unit={m.connection_stats_unit_frames_per_second()}
                  />
                  <Metric
                    title={m.connection_stats_camera_usb_fps()}
                    description={m.connection_stats_camera_usb_fps_description()}
                    stream={cameraHistory}
                    metric="usbFps"
                    map={x => ({
                      date: x.date,
                      metric: x.metric != null ? Math.round(x.metric) : null,
                    })}
                    domain={[0, 60]}
                    unit={m.connection_stats_unit_frames_per_second()}
                  />
                  <Metric
                    title={m.connection_stats_camera_bitrate()}
                    description={m.connection_stats_camera_bitrate_description()}
                    stream={cameraHistory}
                    metric="mbps"
                    map={x => ({
                      date: x.date,
                      metric: x.metric != null ? Math.round(x.metric * 100) / 100 : null,
                    })}
                    domain={[0, 20]}
                    unit={m.connection_stats_unit_megabits_per_second()}
                  />
                  <Metric
                    title={m.connection_stats_camera_dropped()}
                    description={m.connection_stats_camera_dropped_description()}
                    stream={cameraHistory}
                    metric="droppedPerSecond"
                    domain={[0, 30]}
                    unit={m.connection_stats_unit_frames_per_second()}
                  />
                  <div className="space-y-1">
                    {cameraStats && (
                      <>
                        <StatRow
                          label={m.connection_stats_row_source()}
                          value={`${cameraStats.sourceWidth}x${cameraStats.sourceHeight} @ ${Math.round(cameraStats.sourceFps)} fps`}
                        />
                        <StatRow
                          label={m.connection_stats_row_sent_size()}
                          value={`${cameraStats.width}x${cameraStats.height}`}
                        />
                      </>
                    )}
                    {cameraState && (
                      <>
                        <StatRow label={m.connection_stats_row_codec()} value={cameraState.codec} />
                        <StatRow
                          label={m.connection_stats_row_transport()}
                          value={cameraStats?.transport ?? cameraState.transport}
                        />
                        <StatRow
                          label={m.connection_stats_row_usb_format()}
                          value={
                            cameraState.usb_size
                              ? `${cameraState.usb_size} @ ${cameraState.usb_fps || "?"} fps`
                              : m.connection_stats_row_auto()
                          }
                        />
                        <StatRow
                          label={m.connection_stats_row_total_frames()}
                          value={String(cameraState.frames_written ?? 0)}
                        />
                        {cameraState.last_error && (
                          <StatRow
                            label={m.connection_stats_row_last_error()}
                            value={cameraState.last_error}
                          />
                        )}
                      </>
                    )}
                  </div>
                </div>
              )}

              {/* USB microphone */}
              {microphoneActive && (
                <div className="space-y-3">
                  <SettingsSectionHeader
                    title={m.connection_stats_usb_microphone()}
                    description={m.connection_stats_usb_microphone_description()}
                  />
                  <Metric
                    title={m.connection_stats_microphone_written()}
                    description={m.connection_stats_microphone_written_description()}
                    stream={microphoneHistory}
                    metric="kiloSamplesPerSecond"
                    map={x => ({
                      date: x.date,
                      metric: x.metric != null ? Math.round(x.metric * 10) / 10 : null,
                    })}
                    domain={[0, 60]}
                    unit={m.connection_stats_unit_kilosamples_per_second()}
                  />
                  <Metric
                    title={m.connection_stats_microphone_dropped()}
                    description={m.connection_stats_microphone_dropped_description()}
                    stream={microphoneHistory}
                    metric="droppedPerSecond"
                    domain={[0, 4800]}
                    unit={m.connection_stats_unit_samples_per_second()}
                  />
                  <Metric
                    title={m.connection_stats_microphone_level()}
                    description={m.connection_stats_microphone_level_description()}
                    stream={microphoneHistory}
                    metric="level"
                    domain={[0, 100]}
                    unit={m.connection_stats_unit_percent()}
                  />
                  {microphoneState && (
                    <div className="space-y-1">
                      <StatRow
                        label={m.connection_stats_row_sample_rate()}
                        value={`${microphoneState.sampleRate} Hz`}
                      />
                      <StatRow
                        label={m.connection_stats_row_channels()}
                        value={String(microphoneState.channels)}
                      />
                      <StatRow
                        label={m.connection_stats_row_device_ready()}
                        value={
                          microphoneState.deviceReady
                            ? m.connection_stats_row_yes()
                            : m.connection_stats_row_no()
                        }
                      />
                      {microphoneState.lastError && (
                        <StatRow
                          label={m.connection_stats_row_last_error()}
                          value={microphoneState.lastError}
                        />
                      )}
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
