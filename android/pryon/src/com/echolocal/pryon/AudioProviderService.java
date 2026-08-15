package com.echolocal.pryon;

import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.media.AudioFormat;
import android.os.Binder;
import android.os.IBinder;
import android.os.Parcel;
import android.os.Process;
import android.os.RemoteException;
import android.util.Log;

import java.io.File;

import dalvik.system.DexClassLoader;

/** Creates the shared Amazon AudioStream; the native detector is its only writer. */
public final class AudioProviderService extends Service {
    private Object stream;
    private Class<?> streamClass;

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
    }

    @Override
    public void onDestroy() {
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
