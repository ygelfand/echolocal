package com.echolocal.pryon;

import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.media.AudioFormat;
import android.net.Credentials;
import android.net.LocalServerSocket;
import android.net.LocalSocket;
import android.os.Binder;
import android.os.IBinder;
import android.os.Parcel;
import android.os.Process;
import android.os.RemoteException;
import android.util.Log;

import java.io.File;
import java.io.IOException;
import java.io.OutputStream;
import java.lang.reflect.Method;

import dalvik.system.DexClassLoader;

/** Creates the shared Amazon AudioStream; the native detector is its only writer. */
public final class AudioProviderService extends Service {
    private Object stream;
    private Class<?> streamClass;
    private volatile LocalServerSocket pcmServer;
    private volatile boolean stopping;

    private final Binder binder = new Binder() {
        @Override
        protected boolean onTransact(int code, Parcel data, Parcel reply, int flags)
                throws RemoteException {
            try {
                data.enforceInterface(PryonProtocol.DESCRIPTOR);
                if (code == PryonProtocol.GET_STREAM) {
                    ensureStream();
                    reply.writeNoException();
                    streamClass.getMethod("writeToParcel", Parcel.class, int.class)
                            .invoke(stream, reply, 0);
                    return true;
                }
            } catch (Throwable error) {
                Log.e(PryonProtocol.TAG, "PRYON_AUDIO_PROVIDER_ERROR", error);
                reply.writeException(new IllegalStateException(error));
                return true;
            }
            return super.onTransact(code, data, reply, flags);
        }
    };

    @Override
    public IBinder onBind(Intent intent) {
        Log.i(PryonProtocol.TAG, "PRYON_AUDIO_PROVIDER_BOUND");
        return binder;
    }

    private synchronized void ensureStream() throws Exception {
        if (stream != null) return;

        PryonConfig config = PryonConfig.load(this);
        config.validate();
        File dexDir = getDir("amazon_audio_dex", Context.MODE_PRIVATE);
        DexClassLoader loader = new DexClassLoader(
                config.amazonApk, dexDir.getAbsolutePath(), "/system/lib", getClassLoader());
        streamClass = Class.forName("amazon.speech.audio.AudioStream", true, loader);

        AudioFormat format = new AudioFormat.Builder()
                .setSampleRate(16000)
                .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                .setChannelMask(AudioFormat.CHANNEL_IN_MONO)
                .build();
        stream = streamClass.getMethod("create", String.class, AudioFormat.class, int.class)
                .invoke(null, "EchoLocalPryon_Microphone", format, 720000);
        if (stream == null) {
            throw new IllegalStateException("Amazon AudioStream.create returned null");
        }
        Log.i(PryonProtocol.TAG, "PRYON_AUDIO_STREAM_READY sample_rate=16000 channels=1 pcm=16");
        startPcmServer();
    }

    private synchronized void startPcmServer() {
        if (pcmServer != null) return;
        new Thread(new Runnable() {
            @Override
            public void run() {
                servePcm();
            }
        }, "pryon-pcm").start();
    }

    private void servePcm() {
        LocalServerSocket server = null;
        try {
            server = new LocalServerSocket(PryonProtocol.PCM_SOCKET);
            pcmServer = server;
            Log.i(PryonProtocol.TAG, "PRYON_SHARED_PCM_LISTENING socket=@"
                    + PryonProtocol.PCM_SOCKET);
            while (!stopping) {
                LocalSocket socket = server.accept();
                Credentials peer = socket.getPeerCredentials();
                if (peer == null || peer.getUid() != 0) {
                    Log.w(PryonProtocol.TAG, "PRYON_SHARED_PCM_REJECTED uid="
                            + (peer == null ? "unknown" : peer.getUid()));
                    closeQuietly(socket);
                    continue;
                }
                servePcmClient(socket);
            }
        } catch (Throwable error) {
            if (!stopping) Log.e(PryonProtocol.TAG, "PRYON_SHARED_PCM_SERVER_ERROR", error);
        } finally {
            closeQuietly(server);
            pcmServer = null;
        }
    }

    private void servePcmClient(LocalSocket socket) {
        Object reader = null;
        try {
            Object currentStream;
            Class<?> currentClass;
            synchronized (this) {
                currentStream = stream;
                currentClass = streamClass;
            }
            if (currentStream == null || currentClass == null) {
                throw new IllegalStateException("Amazon AudioStream is unavailable");
            }
            reader = currentClass.getMethod("openReader").invoke(currentStream);
            Method synchronize = reader.getClass().getMethod("synchronize");
            long position = ((Number) synchronize.invoke(reader)).longValue();
            Method read = reader.getClass().getMethod(
                    "read", byte[].class, int.class, int.class);
            OutputStream output = socket.getOutputStream();
            byte[] frame = new byte[640];
            boolean first = true;
            Log.i(PryonProtocol.TAG, "PRYON_SHARED_PCM_CONNECTED uid=0 position=" + position);
            while (!stopping) {
                int count = ((Number) read.invoke(reader, frame, 0, frame.length)).intValue();
                if (count < 0) {
                    // The socket can connect in the short interval between AudioStream creation and
                    // the native detector starting its writer. Let the helper reconnect quietly.
                    Log.i(PryonProtocol.TAG, "PRYON_SHARED_PCM_WAITING_FOR_WRITER result=" + count);
                    return;
                }
                if (count == 0) continue;
                output.write(frame, 0, count);
                if (first) {
                    Log.i(PryonProtocol.TAG, "PRYON_SHARED_PCM_FIRST_FRAME bytes=" + count);
                    first = false;
                }
            }
        } catch (IOException error) {
            if (!stopping) Log.i(PryonProtocol.TAG,
                    "PRYON_SHARED_PCM_CLIENT_CLOSED reason=" + error.getMessage());
        } catch (Throwable error) {
            if (!stopping) Log.w(PryonProtocol.TAG, "PRYON_SHARED_PCM_CLIENT_ERROR", error);
        } finally {
            if (reader != null) {
                try {
                    reader.getClass().getMethod("close").invoke(reader);
                } catch (Throwable ignored) { }
            }
            closeQuietly(socket);
            Log.i(PryonProtocol.TAG, "PRYON_SHARED_PCM_DISCONNECTED");
        }
    }

    private static void closeQuietly(LocalSocket socket) {
        if (socket == null) return;
        try { socket.close(); } catch (IOException ignored) { }
    }

    private static void closeQuietly(LocalServerSocket server) {
        if (server == null) return;
        try { server.close(); } catch (IOException ignored) { }
    }

    @Override
    public void onDestroy() {
        stopping = true;
        closeQuietly(pcmServer);
        pcmServer = null;
        stream = null;
        streamClass = null;
        Log.i(PryonProtocol.TAG, "PRYON_AUDIO_PROVIDER_EXIT");
        super.onDestroy();
        // Amazon's audio JNI is process-global and cannot be loaded by a second
        // DexClassLoader in the same ART process. A fresh provider process is the
        // deterministic restart boundary.
        Process.killProcess(Process.myPid());
    }
}
