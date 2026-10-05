"use client";

import { Camera, CheckCircle2, Keyboard, QrCode, RefreshCw, ScanLine, ShieldAlert, XCircle } from "lucide-react";
import jsQR from "jsqr";
import QrScanner from "qr-scanner";
import { type ChangeEvent, useCallback, useEffect, useRef, useState } from "react";
import { api, definitiveFailure, displayError, operationHeaders, operationKey } from "@/lib/api";
import { listAllCheckinTargets } from "@/lib/checkin-targets";
import { formatShanghaiDateTime } from "@/lib/time";
import type { Checkin, CheckinTarget, Verification } from "@/lib/types";

function cameraErrorMessage(reason: unknown): string {
  const name = reason instanceof DOMException ? reason.name : "";
  if (name === "NotAllowedError" || name === "PermissionDeniedError") return "浏览器没有授予摄像头权限，请在地址栏允许摄像头后重试。";
  if (name === "NotFoundError" || name === "DevicesNotFoundError") return "没有找到摄像头，请检查设备连接或改用备份码。";
  if (name === "NotReadableError" || name === "TrackStartError") return "摄像头正被其它应用占用，请关闭微信开发者工具、会议软件后重试。";
  if (name === "SecurityError" || name === "TypeError") return "摄像头需要 HTTPS 页面，请通过 https://localhost:3002 或正式 HTTPS 域名打开后台。";
  return "摄像头启动失败，请检查权限和 HTTPS；也可以改用备份码。";
}

type PendingVerification = {
  version: 1;
  operationKey: string;
  seriesId: string;
  instanceId: string;
  sessionId: string;
  presentedKind: "qr_token" | "backup_code";
  presentedDigest: string;
};

const pendingVerificationKey = "xiangwan.admin.checkin.verification.pending.v1";

const decisionLabels: Record<string, string> = {
  valid: "凭证有效，可以签到",
  already_checked_in: "此人已经签到",
  invalid_credential: "未找到有效凭证",
  expired: "凭证已过期，请用户刷新",
  revoked: "凭证已失效",
  registration_ineligible: "当前报名不符合签到条件",
  wrong_context: "凭证不属于当前场次",
};

async function credentialDigest(value: string): Promise<string> {
  const bytes = new TextEncoder().encode(value);
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export default function CheckinPage() {
  const [targets, setTargets] = useState<CheckinTarget[]>([]);
  const [targetId, setTargetId] = useState("");
  const [kind, setKind] = useState<"qr_token" | "backup_code">("backup_code");
  const [presentedValue, setPresentedValue] = useState("");
  const [verification, setVerification] = useState<Verification | null>(null);
  const [recorded, setRecorded] = useState<Checkin | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [cameraActive, setCameraActive] = useState(false);
  const [scanPhase, setScanPhase] = useState<"idle" | "starting" | "scanning" | "detected" | "verifying">("idle");
  const [capturedFrame, setCapturedFrame] = useState<string | null>(null);
  const [scanHint, setScanHint] = useState("");
  const [flashAvailable, setFlashAvailable] = useState(false);
  const [flashOn, setFlashOn] = useState(false);
  const [recoveryNotice, setRecoveryNotice] = useState("");
  const [frameReady, setFrameReady] = useState(false);
  const [imageScanBusy, setImageScanBusy] = useState(false);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const imageInputRef = useRef<HTMLInputElement | null>(null);
  const qrScannerRef = useRef<QrScanner | null>(null);
  const fallbackCanvasRef = useRef<HTMLCanvasElement | null>(null);
  const fallbackFrameRef = useRef<number | null>(null);
  const fallbackBusyRef = useRef(false);
  const fallbackLastScanAt = useRef(0);
  const decodedValueRef = useRef<string | null>(null);
  const acceptDecodedValueRef = useRef<(rawValue: string, frameOverride?: string | null) => void>(() => {});
  const verifyValueRef = useRef<(value: string, kindOverride?: "qr_token" | "backup_code") => Promise<void>>(async () => {});
  const activeCameraGeneration = useRef(0);
  const verificationOperation = useRef<string | null>(null);
  const pendingVerification = useRef<PendingVerification | null>(null);
  const verificationGeneration = useRef(0);
  const cameraGeneration = useRef(0);
  const cameraStartedAt = useRef(0);
  const frameReadyRef = useRef(false);
  const frameWaitNoticeRef = useRef(false);

  const selected = targets.find((item) => item.session_id === targetId) || null;

  const loadTargets = useCallback(async () => {
    setError("");
    try {
      const values = await listAllCheckinTargets();
      setTargets(values);
      // Targets arrive newest-start-first, so the first row is the furthest
      // future Session rather than the one being staffed. Only a single
      // authorized Session is unambiguous enough to preselect.
      setTargetId((current) => current || (values.length === 1 ? values[0].session_id : ""));
    } catch (reason) {
      setError(displayError(reason));
    }
  }, []);

  useEffect(() => {
    try {
      const restored = JSON.parse(sessionStorage.getItem(pendingVerificationKey) || "null") as PendingVerification | null;
      if (restored?.version === 1 && restored.operationKey && restored.sessionId && restored.presentedDigest) {
        pendingVerification.current = restored;
        verificationOperation.current = restored.operationKey;
        setTargetId(restored.sessionId);
        setKind(restored.presentedKind);
        setRecoveryNotice("上次核验结果尚未确认。请保持当前场次，并重新扫描同一凭证。");
      }
    } catch {
      sessionStorage.removeItem(pendingVerificationKey);
    }
    void loadTargets();
  }, [loadTargets]);

  const stopCamera = useCallback(() => {
    cameraGeneration.current += 1;
    activeCameraGeneration.current = 0;
    if (fallbackFrameRef.current !== null) cancelAnimationFrame(fallbackFrameRef.current);
    fallbackFrameRef.current = null;
    fallbackBusyRef.current = false;
    qrScannerRef.current?.stop();
    setCameraActive(false);
    setScanHint("");
    setFlashAvailable(false);
    setFlashOn(false);
    setFrameReady(false);
    frameReadyRef.current = false;
    frameWaitNoticeRef.current = false;
  }, []);

  function captureCurrentFrame(): string | null {
    const video = videoRef.current;
    if (!video || video.videoWidth < 1 || video.videoHeight < 1) return null;
    const canvas = document.createElement("canvas");
    canvas.width = video.videoWidth;
    canvas.height = video.videoHeight;
    const context = canvas.getContext("2d");
    if (!context) return null;
    context.drawImage(video, 0, 0, canvas.width, canvas.height);
    return canvas.toDataURL("image/jpeg", 0.82);
  }

  function acceptDecodedValue(rawValue: string, frameOverride?: string | null) {
    const value = rawValue.trim();
    const cameraIsActive = activeCameraGeneration.current !== 0 && activeCameraGeneration.current === cameraGeneration.current;
    if (!value || (!cameraIsActive && !frameOverride) || decodedValueRef.current) return;
    decodedValueRef.current = value;
    const frame = frameOverride || captureCurrentFrame();
    if (frame) setCapturedFrame(frame);
    setScanPhase("detected");
    setKind("qr_token");
    setPresentedValue(value);
    stopCamera();
    // Keep the frame visible briefly, like WeChat's capture-and-confirm
    // feedback, so a successful detection is unmistakable to the operator.
    const stoppedGeneration = cameraGeneration.current;
    window.setTimeout(() => {
      if (cameraGeneration.current !== stoppedGeneration) return;
      setScanPhase("verifying");
      void verifyValueRef.current(value, "qr_token");
    }, 180);
  }

  useEffect(() => {
    acceptDecodedValueRef.current = acceptDecodedValue;
  });

  useEffect(() => () => {
    stopCamera();
    qrScannerRef.current?.destroy();
    qrScannerRef.current = null;
  }, [stopCamera]);

  function invalidateVerification(discardPending = true) {
    verificationGeneration.current += 1;
    if (discardPending) {
      verificationOperation.current = null;
      pendingVerification.current = null;
      sessionStorage.removeItem(pendingVerificationKey);
      setRecoveryNotice("");
    }
    setVerification(null);
    setRecorded(null);
  }

  async function startCamera() {
    if (busy) return;
    const requestGeneration = cameraGeneration.current + 1;
    cameraGeneration.current = requestGeneration;
    activeCameraGeneration.current = requestGeneration;
    setError("");
    setPresentedValue("");
    setCapturedFrame(null);
    setScanPhase("starting");
    setFrameReady(false);
    cameraStartedAt.current = 0;
    frameReadyRef.current = false;
    frameWaitNoticeRef.current = false;
    decodedValueRef.current = null;
    fallbackLastScanAt.current = 0;
    setRecoveryNotice("");
    invalidateVerification(false);
    if (!navigator.mediaDevices?.getUserMedia) {
      setError("当前浏览器没有摄像头接口，请使用 HTTPS 浏览器或改用备份码。");
      setScanPhase("idle");
      return;
    }
    const video = videoRef.current;
    if (!video) {
      setError("扫码画面尚未准备好，请刷新页面后重试。");
      setScanPhase("idle");
      return;
    }
    try {
      if (!qrScannerRef.current) {
        let lastDecodeErrorAt = 0;
        qrScannerRef.current = new QrScanner(
          video,
          (result) => {
            if (activeCameraGeneration.current !== cameraGeneration.current) return;
            acceptDecodedValueRef.current(result.data);
          },
          {
            preferredCamera: "environment",
            maxScansPerSecond: 15,
            calculateScanRegion: (currentVideo) => {
              const width = currentVideo.videoWidth;
              const height = currentVideo.videoHeight;
              const scale = Math.min(1, 960 / Math.max(width, height));
              return {
                x: 0,
                y: 0,
                width,
                height,
                downScaledWidth: Math.max(1, Math.round(width * scale)),
                downScaledHeight: Math.max(1, Math.round(height * scale)),
              };
            },
            highlightScanRegion: true,
            highlightCodeOutline: true,
            returnDetailedScanResult: true,
            onDecodeError: () => {
              const now = performance.now();
              if (now - lastDecodeErrorAt < 2500 || activeCameraGeneration.current !== cameraGeneration.current) return;
              lastDecodeErrorAt = now;
              setScanHint("请将完整二维码放入框内，保持画面稳定");
            },
          },
        );
        qrScannerRef.current.setInversionMode("both");
      }
      await qrScannerRef.current.start();
      if (cameraGeneration.current !== requestGeneration) {
        qrScannerRef.current.stop();
        return;
      }
      setCameraActive(true);
      setScanPhase("scanning");
      setScanHint("正在识别二维码…");
      const runFallbackScan = () => {
        if (activeCameraGeneration.current !== requestGeneration || !videoRef.current) return;
        const video = videoRef.current;
        const now = performance.now();
        if (!cameraStartedAt.current) cameraStartedAt.current = now;
        if (video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && video.videoWidth > 0 && video.videoHeight > 0) {
          if (!frameReadyRef.current) {
            frameReadyRef.current = true;
            setFrameReady(true);
          }
        } else if (!frameWaitNoticeRef.current && now - cameraStartedAt.current > 1800) {
          frameWaitNoticeRef.current = true;
          setFrameReady(false);
          setScanHint("摄像头已授权，但还没有收到视频帧；请关闭占用摄像头的程序后重试");
        }
        if (!fallbackBusyRef.current && now - fallbackLastScanAt.current >= 140 && video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && video.videoWidth > 0) {
          fallbackBusyRef.current = true;
          fallbackLastScanAt.current = now;
          try {
            const canvas = fallbackCanvasRef.current || document.createElement("canvas");
            fallbackCanvasRef.current = canvas;
            const scale = Math.min(1, 960 / Math.max(video.videoWidth, video.videoHeight));
            canvas.width = Math.max(1, Math.round(video.videoWidth * scale));
            canvas.height = Math.max(1, Math.round(video.videoHeight * scale));
            const context = canvas.getContext("2d", { willReadFrequently: true });
            if (context) {
              context.drawImage(video, 0, 0, canvas.width, canvas.height);
              const image = context.getImageData(0, 0, canvas.width, canvas.height);
              const result = jsQR(image.data, image.width, image.height, { inversionAttempts: "attemptBoth" });
              if (result?.data) acceptDecodedValue(result.data);
            }
          } catch {
            // A transient frame decode failure is expected while the camera moves.
          } finally {
            fallbackBusyRef.current = false;
          }
        }
        if (activeCameraGeneration.current === requestGeneration) {
          fallbackFrameRef.current = requestAnimationFrame(runFallbackScan);
        }
      };
      fallbackFrameRef.current = requestAnimationFrame(runFallbackScan);
      try {
        setFlashAvailable(await qrScannerRef.current.hasFlash());
      } catch {
        setFlashAvailable(false);
      }
    } catch (reason) {
      stopCamera();
      setScanPhase("idle");
      setError(cameraErrorMessage(reason));
    }
  }

  async function captureAndDecodeCurrentFrame() {
    if (!cameraActive || busy || imageScanBusy) return;
    const video = videoRef.current;
    if (!video || video.videoWidth < 1 || video.videoHeight < 1) {
      setScanHint("当前还没有可用视频帧，请等待画面稳定后再截取");
      return;
    }
    const canvas = document.createElement("canvas");
    canvas.width = video.videoWidth;
    canvas.height = video.videoHeight;
    const context = canvas.getContext("2d", { willReadFrequently: true });
    if (!context) {
      setScanHint("当前浏览器无法读取摄像头画面，请改用二维码图片识别");
      return;
    }
    context.drawImage(video, 0, 0, canvas.width, canvas.height);
    const image = context.getImageData(0, 0, canvas.width, canvas.height);
    const result = jsQR(image.data, image.width, image.height, { inversionAttempts: "attemptBoth" });
    if (!result?.data) {
      setScanHint("这张画面没有识别到二维码，请让二维码完整进入取景框");
      return;
    }
    acceptDecodedValue(result.data, canvas.toDataURL("image/jpeg", 0.86));
  }

  async function decodeImageFile(file: File): Promise<{ data: string; preview: string } | null> {
    const bitmap = await createImageBitmap(file);
    try {
      const scale = Math.min(1, 1400 / Math.max(bitmap.width, bitmap.height));
      const canvas = document.createElement("canvas");
      canvas.width = Math.max(1, Math.round(bitmap.width * scale));
      canvas.height = Math.max(1, Math.round(bitmap.height * scale));
      const context = canvas.getContext("2d", { willReadFrequently: true });
      if (!context) return null;
      context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
      const image = context.getImageData(0, 0, canvas.width, canvas.height);
      const result = jsQR(image.data, image.width, image.height, { inversionAttempts: "attemptBoth" });
      if (result?.data) return { data: result.data, preview: canvas.toDataURL("image/jpeg", 0.86) };
      try {
        const detailed = await QrScanner.scanImage(file, {
          alsoTryWithoutScanRegion: true,
          returnDetailedScanResult: true,
        });
        if (detailed?.data) return { data: detailed.data, preview: canvas.toDataURL("image/jpeg", 0.86) };
      } catch {
        // Keep the user-facing error below; an unreadable image is not a server failure.
      }
      return null;
    } finally {
      bitmap.close();
    }
  }

  async function handleImageSelection(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0] || null;
    event.target.value = "";
    if (!file) return;
    if (!selected) {
      setError("请先选择已授权场次，再识别二维码图片。");
      return;
    }
    if (!file.type.startsWith("image/")) {
      setError("请选择 PNG、JPG 或微信截图等图片文件。");
      return;
    }
    if (file.size > 12 * 1024 * 1024) {
      setError("图片不能超过 12 MB，请选择二维码截图或压缩后的图片。");
      return;
    }
    stopCamera();
    setError("");
    setScanHint("");
    setCapturedFrame(null);
    setPresentedValue("");
    setScanPhase("starting");
    setImageScanBusy(true);
    decodedValueRef.current = null;
    invalidateVerification(false);
    try {
      const result = await decodeImageFile(file);
      if (!result) {
        setScanPhase("idle");
        setError("图片中没有识别到二维码。请确认截图包含完整二维码和四周白边。");
        return;
      }
      acceptDecodedValue(result.data, result.preview);
    } catch {
      setScanPhase("idle");
      setError("读取二维码图片失败，请换一张清晰截图重试。");
    } finally {
      setImageScanBusy(false);
    }
  }

  async function toggleFlash() {
    if (!cameraActive || !flashAvailable || !qrScannerRef.current) return;
    try {
      await qrScannerRef.current.toggleFlash();
      setFlashOn(qrScannerRef.current.isFlashOn());
    } catch {
      setError("当前摄像头不支持闪光灯，请保持环境光线充足。");
    }
  }

  async function verifyValue(value: string, kindOverride = kind) {
    if (!selected) return;
    stopCamera();
    setScanPhase("verifying");
    const submittedTarget = selected;
    const submittedKind = kindOverride;
    const submittedValue = value.trim();
    let digest: string;
    try {
      digest = await credentialDigest(submittedValue);
    } catch {
      setError("当前设备无法完成核验，请更换设备后重试。");
      return;
    }
    const generation = verificationGeneration.current + 1;
    verificationGeneration.current = generation;
    setBusy(true);
    setError("");
    setVerification(null);
    setRecorded(null);
    const recovered = pendingVerification.current;
    const sameRequest = recovered?.seriesId === submittedTarget.series_id &&
      recovered.instanceId === submittedTarget.instance_id &&
      recovered.sessionId === submittedTarget.session_id &&
      recovered.presentedKind === submittedKind && recovered.presentedDigest === digest;
    if (recovered && !sameRequest) {
      // A different credential must not silently overwrite the unknown
      // verification: losing its operation key turns a replay into a second
      // immutable verification attempt. Make the operator discard it
      // explicitly (codex review 2026-09-19, P1).
      const discardPrevious = window.confirm(
        "上一次核验结果仍未知：重新提交同一凭证会自动接续处理。\n改用新凭证将放弃对上一次的重放机会，确定继续？",
      );
      if (!discardPrevious) {
        setBusy(false);
        setPresentedValue("");
        return;
      }
      pendingVerification.current = null;
      verificationOperation.current = null;
      sessionStorage.removeItem(pendingVerificationKey);
      setRecoveryNotice("已放弃上次未确认的核验请求，本次提交使用新的核验操作。");
    }
    const pending: PendingVerification = {
      version: 1,
      operationKey: sameRequest ? recovered.operationKey : operationKey(),
      seriesId: submittedTarget.series_id,
      instanceId: submittedTarget.instance_id,
      sessionId: submittedTarget.session_id,
      presentedKind: submittedKind,
      presentedDigest: digest,
    };
    pendingVerification.current = pending;
    verificationOperation.current = pending.operationKey;
    sessionStorage.setItem(pendingVerificationKey, JSON.stringify(pending));
    try {
      const result = await api<Verification>("/checkin-verifications", {
        method: "POST", headers: operationHeaders(verificationOperation.current),
        body: JSON.stringify({
          series_id: submittedTarget.series_id,
          instance_id: submittedTarget.instance_id,
          session_id: submittedTarget.session_id,
          presented_kind: submittedKind,
          presented_value: submittedValue,
        }),
      });
      if (verificationGeneration.current !== generation) return;
      verificationOperation.current = null;
      pendingVerification.current = null;
      sessionStorage.removeItem(pendingVerificationKey);
      setRecoveryNotice("");
      setVerification(result);
      setPresentedValue("");
    } catch (reason) {
      if (verificationGeneration.current !== generation) return;
      if (definitiveFailure(reason)) {
        verificationOperation.current = null;
        pendingVerification.current = null;
        sessionStorage.removeItem(pendingVerificationKey);
        setRecoveryNotice("");
      } else {
        setRecoveryNotice("核验结果仍未知；请保持当前场次并重新输入同一凭证后重试。");
      }
      setError(displayError(reason));
    } finally {
      setBusy(false);
      if (verificationGeneration.current === generation) {
        setCapturedFrame(null);
        setScanPhase("idle");
      }
    }
  }

  useEffect(() => {
    verifyValueRef.current = verifyValue;
  });

  async function verify(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    await verifyValue(presentedValue);
  }

  async function record() {
    if (!verification?.can_record || !verification.registration_id || !verification.credential_id) return;
    setBusy(true);
    setError("");
    try {
      const result = await api<Checkin>(`/registrations/${verification.registration_id}/checkins`, {
        method: "POST",
        body: JSON.stringify({
          series_id: verification.series_id,
          instance_id: verification.instance_id,
          session_id: verification.session_id,
          credential_id: verification.credential_id,
          verification_attempt_id: verification.verification_attempt_id,
        }),
      });
      setRecorded(result);
      setVerification(null);
      await loadTargets();
    } catch (reason) {
      setError(displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  // already_checked_in is a legitimate credential that was already recorded.
  // It carries can_record false, so it must not render as a refusal.
  const alreadyCheckedIn = verification?.decision === "already_checked_in";
  const verificationAccepted = Boolean(verification?.can_record) || alreadyCheckedIn;

  return (
    <div className="page-content checkin-page">
      <section className="checkin-stats">
        <article><span>已报名</span><strong>{selected?.confirmed_registration_count ?? 0}</strong><small>当前授权场次</small></article>
        <article><span>已签到</span><strong>{selected?.checked_in_registration_count ?? 0}</strong><small>实时签到人数</small></article>
        <article><span>未签到</span><strong>{selected ? Math.max(0, selected.confirmed_registration_count - selected.checked_in_registration_count) : 0}</strong><small>不含取消报名</small></article>
      </section>

      <section className="scan-panel">
        <div className="scan-heading"><span className="scan-icon"><ScanLine /></span><div><h2>扫码签到</h2><p>扫描凭证后，请核对结果并确认签到。</p></div></div>
        <label className="target-select">签到场次 <span className="required-mark" aria-hidden="true">*</span><select value={targetId} disabled={busy} onChange={(event) => { stopCamera(); setScanPhase("idle"); setCapturedFrame(null); setTargetId(event.target.value); setPresentedValue(""); invalidateVerification(); }} required={true} aria-required={true}><option value="">选择已授权场次</option>{targets.map((target) => <option key={target.session_id} value={target.session_id}>{target.instance_title} · {target.session_title}</option>)}</select></label>
        {recoveryNotice && <div className="inline-message warning">{recoveryNotice}</div>}
        {selected && <div className="target-summary"><b>{selected.series_title}</b><span>{formatShanghaiDateTime(selected.session_start_at)} · {selected.venue_name || "线上活动"}</span></div>}
        <div className={`camera-frame ${cameraActive ? "active" : ""}`}>
          <video ref={videoRef} playsInline autoPlay muted />
          {capturedFrame && !cameraActive && <div className="camera-capture" role="img" aria-label="已捕获二维码画面" style={{ backgroundImage: `url(${capturedFrame})` }} />}
          {!cameraActive && !capturedFrame && <QrCode />}
          {cameraActive && flashAvailable && <button className="flash-button" type="button" onClick={() => void toggleFlash()} aria-label={flashOn ? "关闭闪光灯" : "打开闪光灯"}>{flashOn ? "关灯" : "补光"}</button>}
          <span aria-live="polite">{cameraActive ? (scanHint || (frameReady ? "视频帧已收到，正在识别二维码…" : "已授权，等待视频帧…")) : scanPhase === "starting" ? "正在打开摄像头…" : scanPhase === "detected" ? "已识别二维码，正在截取画面…" : scanPhase === "verifying" ? "已截取画面，正在核验…" : capturedFrame ? "已截取画面，正在核验…" : "摄像头未启动"}</span>
        </div>
        {cameraActive && <div className={`scan-health ${frameReady ? "ready" : "waiting"}`} role="status">
          <span className="health-dot" />{frameReady ? "摄像头画面已进入识别器" : "摄像头已打开，等待画面"}
        </div>}
        <button
          className="secondary-button wide"
          type="button"
          onClick={() => {
            if (cameraActive) {
              stopCamera();
              setScanPhase("idle");
              setCapturedFrame(null);
              return;
            }
            if (!selected) {
              setError("请先选择已授权场次，再开始扫码。");
              return;
            }
            void startCamera();
          }}
          disabled={busy || imageScanBusy || (!cameraActive && scanPhase !== "idle")}
        >
          {cameraActive ? <><XCircle /> 停止扫码</> : <><Camera /> 开始扫码</>}
        </button>
        {cameraActive && <button className="secondary-button wide" type="button" onClick={() => void captureAndDecodeCurrentFrame()} disabled={busy || imageScanBusy}>
          <ScanLine /> 截取当前画面识别
        </button>}
        <input ref={imageInputRef} className="visually-hidden" type="file" accept="image/*" onChange={(event) => void handleImageSelection(event)} />
        <button className="secondary-button wide" type="button" onClick={() => imageInputRef.current?.click()} disabled={busy || imageScanBusy || cameraActive || scanPhase !== "idle"}>
          <QrCode /> {imageScanBusy ? "正在识别图片…" : "选择二维码图片识别"}
        </button>
        <div className="or-divider"><span>或使用备份码</span></div>
        <form className="manual-checkin" onSubmit={verify}>
          <div className="segmented compact"><button type="button" disabled={busy} className={kind === "backup_code" ? "active" : ""} onClick={() => { setKind("backup_code"); setPresentedValue(""); invalidateVerification(); }}><Keyboard /> 备份码</button><button type="button" disabled={busy} className={kind === "qr_token" ? "active" : ""} onClick={() => { setKind("qr_token"); setPresentedValue(""); invalidateVerification(); }}><QrCode /> 扫码结果</button></div>
          <label><span>{kind === "backup_code" ? "输入用户备份码" : "扫码结果"} <span className="required-mark" aria-hidden="true">*</span></span><input type="password" autoComplete="off" value={presentedValue} disabled={busy} onChange={(event) => { setPresentedValue(event.target.value); invalidateVerification(false); }} placeholder={kind === "backup_code" ? "区分大小写" : "扫码内容已隐藏"} maxLength={512} required aria-required={true} /></label>
          <button className="primary-button wide" disabled={!selected || !presentedValue.trim() || busy}><ShieldAlert /> {busy ? "正在核验…" : "核验签到凭证"}</button>
        </form>
      </section>

      <aside className="verification-panel">
        <div className="panel-head"><div><h2>核验结果</h2><p>为保护隐私，这里不会显示姓名或手机号。</p></div><button className="icon-button" disabled={busy} onClick={() => { invalidateVerification(); setError(""); }} aria-label="清空结果"><RefreshCw /></button></div>
        {error && <div className="inline-message error">{error}</div>}
        {recorded ? <div className="result-card success"><CheckCircle2 /><h3>签到成功</h3><p>{formatShanghaiDateTime(recorded.checked_in_at)}</p><small>{recorded.duplicate ? "这次签到已经记录，无需重复操作" : "签到记录已保存"}</small></div> : verification ? <div className={`result-card ${verificationAccepted ? (alreadyCheckedIn ? "success" : "valid") : "rejected"}`}>
          {verificationAccepted ? <CheckCircle2 /> : <XCircle />}
          <h3>{verification.can_record ? "凭证有效" : alreadyCheckedIn ? "此人已签到" : "无法签到"}</h3>
          <p>{decisionLabels[verification.decision] || "请重新核对签到凭证"}</p>
          <small>核验完成后不会保留凭证内容</small>
          {verification.can_record && <button className="primary-button wide" onClick={() => void record()} disabled={busy}>确认签到</button>}
        </div> : <div className="result-placeholder"><QrCode /><p>完成扫码或输入备份码后，核验结果会显示在这里。</p></div>}
      </aside>
    </div>
  );
}
