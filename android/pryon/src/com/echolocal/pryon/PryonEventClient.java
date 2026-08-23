package com.echolocal.pryon;

import android.net.LocalSocket;
import android.net.LocalSocketAddress;
import android.os.SystemClock;
import android.util.Log;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.Charset;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

/** Bounded, wake-only transport to echod's authenticated filesystem socket. */
final class PryonEventClient implements AutoCloseable {
    private static final String SOCKET_NAME = "echolocal-pryon";
    private static final Charset UTF8 = Charset.forName("UTF-8");
    private static final long MAX_EVENT_AGE_MS = 5000;

    private final ArrayBlockingQueue<Pending> queue = new ArrayBlockingQueue<>(8);
    private final AtomicBoolean running = new AtomicBoolean(true);
    private final Thread worker;

    private LocalSocket socket;
    private InputStream input;
    private OutputStream output;

    PryonEventClient() {
        worker = new Thread(new Runnable() {
            @Override
            public void run() {
                work();
            }
        }, "pryon-event-client");
        worker.start();
    }

    void sendWake(int confidence, int detectionType, long monotonicMs) {
        String json = "{\"version\":1,\"event\":\"wake\",\"word\":\"alexa\""
                + ",\"confidence\":" + confidence
                + ",\"detection_type\":" + detectionType
                + ",\"monotonic_ms\":" + monotonicMs + "}\n";
        Pending pending = new Pending(json.getBytes(UTF8), monotonicMs + MAX_EVENT_AGE_MS);
        if (!queue.offer(pending)) {
            queue.poll();
            if (!queue.offer(pending)) {
                Log.w(PryonProtocol.TAG, "PRYON_EVENT_DROPPED reason=queue_full");
            }
        }
    }

    private void work() {
        while (running.get()) {
            Pending pending;
            try {
                pending = queue.poll(1, TimeUnit.SECONDS);
            } catch (InterruptedException interrupted) {
                continue;
            }
            if (pending == null) continue;

            long waitMs = 200;
            while (running.get() && SystemClock.elapsedRealtime() <= pending.expiresAtMs) {
                try {
                    connect();
                    output.write(pending.bytes);
                    output.flush();
                    String acknowledgement = readLine(input);
                    if (!"ok".equals(acknowledgement)) {
                        throw new IOException("helper replied " + acknowledgement);
                    }
                    Log.i(PryonProtocol.TAG, "PRYON_EVENT_DELIVERED event=wake word=alexa");
                    pending = null;
                    break;
                } catch (IOException error) {
                    disconnect();
                    Log.w(PryonProtocol.TAG, "PRYON_EVENT_RETRY in_ms=" + waitMs
                            + " error=" + error.getMessage());
                    SystemClock.sleep(waitMs);
                    waitMs = Math.min(waitMs * 2, 1000);
                }
            }
            if (pending != null) {
                Log.w(PryonProtocol.TAG, "PRYON_EVENT_DROPPED reason=delivery_timeout");
            }
        }
        disconnect();
    }

    private synchronized void connect() throws IOException {
        if (!running.get()) throw new IOException("client is stopping");
        if (socket != null && output != null) return;
        LocalSocket next = new LocalSocket();
        try {
            next.connect(new LocalSocketAddress(
                    SOCKET_NAME, LocalSocketAddress.Namespace.ABSTRACT));
            next.setSoTimeout(1000);
            socket = next;
            input = next.getInputStream();
            output = next.getOutputStream();
            Log.i(PryonProtocol.TAG, "PRYON_EVENT_CONNECTED socket=@" + SOCKET_NAME);
        } catch (IOException error) {
            try {
                next.close();
            } catch (IOException ignored) {
                // The original connect error is the useful one.
            }
            throw error;
        }
    }

    private synchronized void disconnect() {
        input = null;
        output = null;
        if (socket != null) {
            try {
                socket.close();
            } catch (IOException ignored) {
                // Closing a failed local connection has no recovery action.
            }
            socket = null;
        }
    }

    private static String readLine(InputStream input) throws IOException {
        StringBuilder line = new StringBuilder();
        while (line.length() <= 16) {
            int value = input.read();
            if (value < 0) throw new IOException("helper closed without acknowledgement");
            if (value == '\n') return line.toString();
            line.append((char) value);
        }
        throw new IOException("helper acknowledgement too large");
    }

    @Override
    public void close() {
        if (!running.compareAndSet(true, false)) return;
        worker.interrupt();
        disconnect();
        try {
            worker.join(2000);
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        }
    }

    private static final class Pending {
        final byte[] bytes;
        final long expiresAtMs;

        Pending(byte[] bytes, long expiresAtMs) {
            this.bytes = bytes;
            this.expiresAtMs = expiresAtMs;
        }
    }
}
