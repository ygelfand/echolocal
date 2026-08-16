package echolocal;

import android.media.AudioFormat;
import android.media.AudioManager;
import android.media.AudioTrack;
import android.net.Credentials;
import android.net.LocalServerSocket;
import android.net.LocalSocket;
import android.net.LocalSocketAddress;
import android.util.Log;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.EOFException;
import java.io.File;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.Charset;
import java.util.Arrays;
import java.util.HashSet;
import java.util.Iterator;
import java.util.Set;

/** Android media bridge used by the deployed EchoLocal runtime. */
public final class AmazonHelper {
    static final int SAMPLE_RATE = 16000;
    static final int PLAY_RATE = 48000;
    static final int FRAME_BYTES = 640;
    static final int MAX_PAYLOAD = 1024 * 1024;

    static final int MSG_WAKE = 1;
    static final int MSG_AUDIO = 2;
    static final int MSG_START_CAPTURE = 3;
    static final int MSG_STOP_CAPTURE = 4;
    static final int MSG_PLAY = 5;
    static final int MSG_PLAY_STOP = 6;

    static final String SOCKET = "echolocal-amazon";
    static final String PRYON_SOCKET = "echolocal-pryon";
    static final String PRYON_PCM_SOCKET = "echolocal-pryon-pcm";
    static final String PRYON_UID_PATH = "/data/misc/echolocal/pryon.uid";
    static final String TAG = "echolocal-helper";
    static final int MAX_EVENT_BYTES = 1024;

    private AmazonHelper() { }

    public static void main(String[] args) {
        Log.i(TAG, "starting with shared Pryon PCM capture");
        new Server().run();
    }

    static final class Server {
        private volatile Connection current;
        private long lastPryonMonotonicMs = -1;

        void run() {
            Thread pryon = new Thread(new Runnable() {
                @Override
                public void run() {
                    servePryon();
                }
            }, "pryon-events");
            pryon.setDaemon(true);
            pryon.start();

            while (true) {
                LocalServerSocket server = null;
                try {
                    server = new LocalServerSocket(SOCKET);
                    Log.i(TAG, "listening on abstract socket @" + SOCKET);
                    while (true) {
                        LocalSocket socket = server.accept();
                        Log.i(TAG, "echod connected");
                        Connection connection = new Connection(socket);
                        current = connection;
                        connection.serve();
                        if (current == connection) current = null;
                        Log.i(TAG, "echod disconnected");
                    }
                } catch (IOException error) {
                    Log.e(TAG, "server socket error: " + error);
                    closeQuietly(server);
                    sleep(2000);
                }
            }
        }

        private void servePryon() {
            while (true) {
                LocalServerSocket server = null;
                try {
                    server = new LocalServerSocket(PRYON_SOCKET);
                    Log.i(TAG, "listening for Pryon events on @" + PRYON_SOCKET);
                    while (true) {
                        LocalSocket socket = server.accept();
                        if (!authorized(socket)) {
                            closeQuietly(socket);
                            continue;
                        }
                        Log.i(TAG, "Pryon companion connected");
                        servePryonConnection(socket);
                        Log.i(TAG, "Pryon companion disconnected");
                    }
                } catch (IOException error) {
                    Log.e(TAG, "Pryon socket error: " + error);
                    closeQuietly(server);
                    sleep(2000);
                }
            }
        }

        private boolean authorized(LocalSocket socket) {
            try {
                int expected = readUID();
                Credentials peer = socket.getPeerCredentials();
                if (peer == null || peer.getUid() != expected) {
                    Log.w(TAG, "Pryon peer rejected uid="
                            + (peer == null ? "unknown" : peer.getUid())
                            + " expected=" + expected);
                    return false;
                }
                return true;
            } catch (Throwable error) {
                Log.w(TAG, "Pryon peer credentials unavailable: " + error);
                return false;
            }
        }

        private void servePryonConnection(LocalSocket socket) {
            try {
                InputStream input = socket.getInputStream();
                DataOutputStream output = new DataOutputStream(socket.getOutputStream());
                while (true) {
                    byte[] frame = readLine(input);
                    if (frame == null) return;
                    boolean accepted;
                    try {
                        accepted = forwardPryon(new JSONObject(new String(frame, UTF8)));
                    } catch (Throwable error) {
                        Log.w(TAG, "Pryon event rejected: " + error);
                        accepted = false;
                    }
                    output.write(accepted ? ACK_OK : ACK_RETRY);
                    output.flush();
                }
            } catch (IOException error) {
                Log.w(TAG, "Pryon connection error: " + error);
            } finally {
                closeQuietly(socket);
            }
        }

        private synchronized boolean forwardPryon(JSONObject event) throws Exception {
            validateKeys(event);
            if (event.getInt("version") != 1) throw new IOException("bad version");
            if (!"wake".equals(event.getString("event"))) throw new IOException("bad event");
            if (!"alexa".equalsIgnoreCase(event.getString("word"))) {
                throw new IOException("bad word");
            }
            int confidence = event.getInt("confidence");
            if (confidence < 0 || confidence > 1000) throw new IOException("bad confidence");
            int detectionType = event.getInt("detection_type");
            long monotonicMs = event.getLong("monotonic_ms");
            if (monotonicMs <= 0) throw new IOException("bad monotonic_ms");

            if (monotonicMs == lastPryonMonotonicMs) return true;
            Connection connection = current;
            if (connection == null) {
                Log.w(TAG, "Pryon wake waiting for echod");
                return false;
            }
            if (!connection.sendWake(phrase("Alexa"))) return false;
            lastPryonMonotonicMs = monotonicMs;
            Log.i(TAG, "Pryon wake forwarded confidence=" + confidence
                    + " detection_type=" + detectionType);
            return true;
        }
    }

    static final class Connection {
        private volatile boolean capturing;
        private volatile LocalSocket captureSocket;
        private final DataInputStream in;
        private final DataOutputStream out;
        private final LocalSocket socket;
        private AudioTrack track;

        Connection(LocalSocket socket) throws IOException {
            this.socket = socket;
            in = new DataInputStream(socket.getInputStream());
            out = new DataOutputStream(socket.getOutputStream());
        }

        void serve() {
            try {
                while (true) {
                    int type = in.readUnsignedByte();
                    int length = in.readInt();
                    if (length < 0 || length > MAX_PAYLOAD) {
                        throw new IOException("bad frame length " + length);
                    }
                    byte[] payload = new byte[length];
                    in.readFully(payload);
                    switch (type) {
                    case MSG_START_CAPTURE:
                        startCapture();
                        break;
                    case MSG_STOP_CAPTURE:
                        stopCapture();
                        break;
                    case MSG_PLAY:
                        play(payload);
                        break;
                    case MSG_PLAY_STOP:
                        playStop();
                        break;
                    default:
                        Log.w(TAG, "unknown echod message " + type);
                    }
                }
            } catch (EOFException ignored) {
                // Normal disconnect.
            } catch (IOException error) {
                Log.w(TAG, "connection read error: " + error);
            } finally {
                stopCapture();
                playStop();
                closeQuietly(socket);
            }
        }

        synchronized boolean send(int type, byte[] payload) {
            try {
                out.writeByte(type);
                out.writeInt(payload == null ? 0 : payload.length);
                if (payload != null && payload.length > 0) out.write(payload);
                out.flush();
                return true;
            } catch (IOException error) {
                Log.w(TAG, "send failed: " + error);
                return false;
            }
        }

        boolean sendWake(byte[] payload) { return send(MSG_WAKE, payload); }

        private synchronized void startCapture() {
            if (capturing) return;
            capturing = true;
            new Thread(new Runnable() {
                @Override
                public void run() {
                    capture();
                }
            }, "capture").start();
            Log.i(TAG, "capture started");
        }

        private synchronized void stopCapture() {
            capturing = false;
            closeQuietly(captureSocket);
            captureSocket = null;
        }

        private void capture() {
            int failures = 0;
            while (capturing) {
                LocalSocket shared = null;
                try {
                    shared = new LocalSocket();
                    // The timeout overload throws UnsupportedOperationException on Fire OS 5.
                    // This is a local abstract socket, and the outer loop already retries a failed
                    // connection, so use the API-22-compatible overload.
                    shared.connect(new LocalSocketAddress(
                            PRYON_PCM_SOCKET, LocalSocketAddress.Namespace.ABSTRACT));
                    captureSocket = shared;
                    InputStream input = shared.getInputStream();
                    Log.i(TAG, "shared Pryon capture connected");
                    failures = 0;
                    byte[] frame = new byte[FRAME_BYTES];
                    boolean first = true;
                    while (capturing) {
                        int count = input.read(frame, 0, frame.length);
                        if (count < 0) throw new EOFException("shared Pryon PCM ended");
                        if (count == 0) continue;
                        if (!send(MSG_AUDIO, Arrays.copyOf(frame, count))) break;
                        if (first) {
                            Log.i(TAG, "shared Pryon capture first frame bytes=" + count);
                            first = false;
                        }
                    }
                } catch (IOException error) {
                    failures++;
                    if (capturing && (failures == 1 || failures % 20 == 0)) {
                        Log.i(TAG, "waiting for shared Pryon capture attempt=" + failures
                                + " reason=" + error.getMessage());
                    }
                } catch (Throwable error) {
                    if (capturing) Log.w(TAG, "shared Pryon capture error", error);
                } finally {
                    if (captureSocket == shared) captureSocket = null;
                    closeQuietly(shared);
                }
                if (capturing) sleep(250);
            }
            Log.i(TAG, "shared Pryon capture stopped");
        }

        private void play(byte[] payload) {
            if (payload.length == 0) return;
            if (track == null) {
                try {
                    int buffer = Math.max(AudioTrack.getMinBufferSize(PLAY_RATE,
                            AudioFormat.CHANNEL_OUT_STEREO, AudioFormat.ENCODING_PCM_16BIT),
                            payload.length * 8);
                    AudioTrack candidate = new AudioTrack(AudioManager.STREAM_MUSIC, PLAY_RATE,
                            AudioFormat.CHANNEL_OUT_STEREO, AudioFormat.ENCODING_PCM_16BIT,
                            buffer, AudioTrack.MODE_STREAM);
                    if (candidate.getState() != AudioTrack.STATE_INITIALIZED) {
                        Log.e(TAG, "AudioTrack not initialized");
                        candidate.release();
                        return;
                    }
                    candidate.play();
                    track = candidate;
                    Log.i(TAG, "playback started");
                } catch (Throwable error) {
                    Log.e(TAG, "AudioTrack ctor failed: " + error);
                    return;
                }
            }
            track.write(payload, 0, payload.length);
        }

        private void playStop() {
            AudioTrack old = track;
            track = null;
            if (old == null) return;
            try {
                old.pause();
                old.flush();
                old.stop();
            } catch (Throwable ignored) { }
            old.release();
            Log.i(TAG, "playback stopped");
        }
    }

    static final Charset UTF8 = Charset.forName("UTF-8");
    static final byte[] ACK_OK = "ok\n".getBytes(UTF8);
    static final byte[] ACK_RETRY = "retry\n".getBytes(UTF8);
    static final Set<String> EVENT_KEYS = new HashSet<>(Arrays.asList(
            "version", "event", "word", "confidence", "detection_type", "monotonic_ms"));

    static byte[] readLine(InputStream input) throws IOException {
        ByteArrayOutputStream line = new ByteArrayOutputStream();
        while (true) {
            int value = input.read();
            if (value < 0) return line.size() == 0 ? null : line.toByteArray();
            if (value == '\n') return line.toByteArray();
            if (line.size() >= MAX_EVENT_BYTES) throw new IOException("Pryon event too large");
            line.write(value);
        }
    }

    static void validateKeys(JSONObject event) throws IOException {
        Set<String> found = new HashSet<>();
        Iterator<String> keys = event.keys();
        while (keys.hasNext()) found.add(keys.next());
        if (!found.equals(EVENT_KEYS)) throw new IOException("unexpected fields " + found);
    }

    static int readUID() throws IOException {
        File file = new File(PRYON_UID_PATH);
        if (!file.isFile()) throw new IOException("missing " + PRYON_UID_PATH);
        FileInputStream input = new FileInputStream(file);
        try {
            byte[] raw = new byte[32];
            int count = input.read(raw);
            int uid = Integer.parseInt(new String(raw, 0, Math.max(count, 0), UTF8).trim());
            if (uid < 10000) throw new IOException("invalid UID " + uid);
            return uid;
        } catch (NumberFormatException error) {
            throw new IOException("invalid UID", error);
        } finally {
            input.close();
        }
    }

    static byte[] phrase(String value) {
        byte[] text = value.getBytes(UTF8);
        ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        DataOutputStream output = new DataOutputStream(bytes);
        try {
            output.writeShort(text.length);
            output.write(text);
            output.writeInt(0);
            output.writeLong(0);
            output.writeLong(0);
            return bytes.toByteArray();
        } catch (IOException impossible) {
            return new byte[0];
        }
    }

    static void sleep(long milliseconds) {
        try {
            Thread.sleep(milliseconds);
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
        }
    }

    static void closeQuietly(LocalServerSocket socket) {
        if (socket == null) return;
        try { socket.close(); } catch (IOException ignored) { }
    }

    static void closeQuietly(LocalSocket socket) {
        if (socket == null) return;
        try { socket.close(); } catch (IOException ignored) { }
    }
}
