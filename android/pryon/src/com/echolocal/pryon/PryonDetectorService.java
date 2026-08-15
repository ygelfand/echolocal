package com.echolocal.pryon;

import android.app.Service;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.ServiceConnection;
import android.media.AudioFormat;
import android.os.IBinder;
import android.os.Parcel;
import android.os.Parcelable;
import android.os.Process;
import android.os.SystemClock;
import android.util.Log;

import java.io.File;
import java.lang.reflect.Constructor;
import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.util.Arrays;
import java.util.concurrent.atomic.AtomicBoolean;

import dalvik.system.DexClassLoader;

/** Wake-only companion for the firmware-owned Pryon detector. */
public final class PryonDetectorService extends Service {
    private final AtomicBoolean initializing = new AtomicBoolean();
    private final Object nativeLock = new Object();

    private volatile IBinder audioProvider;
    private volatile boolean bindRequested;
    private volatile boolean initialized;
    private volatile boolean nativeCreated;
    private Object core;
    private Class<?> coreClass;
    private Object inputStream;
    private Object metadataStream;
    private Object[] callbackProxies;
    private Class<?> streamClass;
    private long lastDetectionMs;
    private PryonEventClient eventClient;

    @Override
    public void onCreate() {
        super.onCreate();
        eventClient = new PryonEventClient();
        Log.i(PryonProtocol.TAG, "PRYON_SERVICE_CREATED");
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    private final ServiceConnection connection = new ServiceConnection() {
        @Override
        public void onServiceConnected(ComponentName name, IBinder service) {
            audioProvider = service;
            Log.i(PryonProtocol.TAG, "PRYON_AUDIO_PROVIDER_CONNECTED component=" + name);
            initializeAsync();
        }

        @Override
        public void onServiceDisconnected(ComponentName name) {
            Log.w(PryonProtocol.TAG, "PRYON_AUDIO_PROVIDER_DISCONNECTED component=" + name);
            audioProvider = null;
            bindRequested = false;
            cleanupNative("audio_provider_disconnected");
        }
    };

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        try {
            boolean configChanged = PryonConfig.updateFromIntent(this, intent);
            PryonConfig config = PryonConfig.load(this);
            config.validate();
            Log.i(PryonProtocol.TAG, "PRYON_CONFIG " + config.describe());

            if (configChanged && (initialized || core != null)) {
                cleanupNative("configuration_changed");
            }
            startOrBind();
        } catch (Throwable error) {
            Log.e(PryonProtocol.TAG, "PRYON_CONFIG_ERROR", error);
            stopSelf(startId);
        }
        return START_STICKY;
    }

    private void startOrBind() {
        if (audioProvider != null) {
            initializeAsync();
            return;
        }
        if (!bindRequested) {
            bindRequested = bindService(new Intent(this, AudioProviderService.class), connection,
                    Context.BIND_AUTO_CREATE);
            if (!bindRequested) {
                throw new IllegalStateException("Unable to bind AudioProviderService");
            }
        }
    }

    private void initializeAsync() {
        if (initialized || !initializing.compareAndSet(false, true)) return;
        new Thread(new Runnable() {
            @Override
            public void run() {
                try {
                    initialize();
                } catch (Throwable error) {
                    Log.e(PryonProtocol.TAG, "PRYON_INITIALIZATION_FAILED", error);
                    cleanupNative("initialization_failed");
                    stopSelf();
                } finally {
                    initializing.set(false);
                }
            }
        }, "pryon-initialize").start();
    }

    private void initialize() throws Exception {
        PryonConfig config = PryonConfig.load(this);
        config.validate();
        IBinder provider = audioProvider;
        if (provider == null) throw new IllegalStateException("Audio provider is unavailable");

        File dexDir = getDir("amazon_detector_dex", Context.MODE_PRIVATE);
        DexClassLoader loader = new DexClassLoader(
                config.amazonApk, dexDir.getAbsolutePath(), "/system/lib", getClassLoader());
        streamClass = Class.forName("amazon.speech.audio.AudioStream", true, loader);
        inputStream = requestAudioStream(provider);

        AudioFormat metadataFormat = new AudioFormat.Builder()
                .setSampleRate(16000)
                .setEncoding(AudioFormat.ENCODING_PCM_8BIT)
                .setChannelMask(AudioFormat.CHANNEL_IN_MONO)
                .build();
        metadataStream = streamClass.getMethod("create", String.class, AudioFormat.class, int.class)
                .invoke(null, "EchoLocalPryon_Metadata", metadataFormat, 40000);
        if (metadataStream == null) {
            throw new IllegalStateException("Metadata AudioStream.create returned null");
        }

        coreClass = Class.forName(
                "amazon.speech.wakewordservice.NativeWakeWordServiceCore", true, loader);
        Constructor<?> constructor = findCallbackConstructor(coreClass);
        Class<?>[] callbackTypes = constructor.getParameterTypes();
        callbackProxies = new Object[callbackTypes.length];
        for (int i = 0; i < callbackTypes.length; i++) {
            callbackProxies[i] = Proxy.newProxyInstance(
                    loader, new Class<?>[]{callbackTypes[i]},
                    new PryonCallback(callbackTypes[i].getName()));
        }
        constructor.setAccessible(true);
        core = constructor.newInstance(callbackProxies);

        Method create = findMethod(coreClass, "nCreateNativeService", 16);
        create.setAccessible(true);
        int result = ((Number) create.invoke(core,
                getPackageName(), inputStream, metadataStream,
                config.alexaModel, config.aedModel, null, new String[]{"ALEXA"},
                false, false, false, false, false, false, 10, -1, 0)).intValue();
        Log.i(PryonProtocol.TAG, "PRYON_NATIVE_CREATE result=" + result);
        if (result != 0) throw new IllegalStateException("nCreateNativeService result=" + result);
        nativeCreated = true;

        Method enable = findMethod(coreClass, "nSetDetectorEnabled", 1);
        enable.setAccessible(true);
        int enableResult = ((Number) enable.invoke(core, true)).intValue();
        Method getEnabled = findMethod(coreClass, "nGetDetectorEnabled", 0);
        getEnabled.setAccessible(true);
        boolean enabled = (Boolean) getEnabled.invoke(core);
        if (enableResult != 0 || !enabled) {
            throw new IllegalStateException(
                    "Detector enable failed result=" + enableResult + " enabled=" + enabled);
        }

        initialized = true;
        Log.i(PryonProtocol.TAG,
                "PRYON_READY enabled=true recorder=HOTWORD sample_rate=16000 word=alexa");
    }

    private Object requestAudioStream(IBinder provider) throws Exception {
        Parcel data = Parcel.obtain();
        Parcel reply = Parcel.obtain();
        try {
            data.writeInterfaceToken(PryonProtocol.DESCRIPTOR);
            if (!provider.transact(PryonProtocol.GET_STREAM, data, reply, 0)) {
                throw new IllegalStateException("GET_STREAM transaction was rejected");
            }
            reply.readException();
            Parcelable.Creator<?> creator =
                    (Parcelable.Creator<?>) streamClass.getField("CREATOR").get(null);
            Object value = creator.createFromParcel(reply);
            if (value == null) throw new IllegalStateException("Provider returned a null AudioStream");
            return value;
        } finally {
            data.recycle();
            reply.recycle();
        }
    }

    private final class PryonCallback implements InvocationHandler {
        private final String callbackType;

        PryonCallback(String callbackType) {
            this.callbackType = callbackType;
        }

        @Override
        public Object invoke(Object proxy, Method method, Object[] args) {
            String methodName = method.getName();
            if ("onEnumeratedResult".equals(methodName) && args != null && args.length > 7) {
                String result = String.valueOf(args[1]);
                int confidence = args[6] instanceof Number ? ((Number) args[6]).intValue() : 0;
                int detectionType = args[7] instanceof Number ? ((Number) args[7]).intValue() : 0;
                Log.i(PryonProtocol.TAG, "PRYON_RESULT word=" + result.toLowerCase()
                        + " confidence=" + confidence + " detection_type=" + detectionType);
                if ("ALEXA".equalsIgnoreCase(result) && detectionType != 1) {
                    dispatchAlexa(confidence, detectionType);
                }
            } else if (methodName.toLowerCase().contains("status")) {
                Log.i(PryonProtocol.TAG, "PRYON_STATUS callback=" + callbackType
                        + " method=" + methodName + " args=" + Arrays.toString(args));
            }
            return defaultValue(method.getReturnType());
        }
    }

    private synchronized void dispatchAlexa(int confidence, int detectionType) {
        long now = SystemClock.elapsedRealtime();
        if (now - lastDetectionMs < 1500) {
            Log.i(PryonProtocol.TAG, "PRYON_DUPLICATE_SUPPRESSED delta_ms="
                    + (now - lastDetectionMs));
            return;
        }
        lastDetectionMs = now;
        Log.i(PryonProtocol.TAG, "PRYON_WAKEWORD_DETECTED word=alexa confidence=" + confidence
                + " detection_type=" + detectionType + " monotonic_ms=" + now);
        eventClient.sendWake(confidence, detectionType, now);
    }

    private void cleanupNative(String reason) {
        synchronized (nativeLock) {
            initialized = false;
            if (nativeCreated && core != null && coreClass != null) {
                try {
                    Method disable = findMethod(coreClass, "nSetDetectorEnabled", 1);
                    disable.setAccessible(true);
                    Object result = disable.invoke(core, false);
                    Log.i(PryonProtocol.TAG, "PRYON_NATIVE_DISABLED result=" + result
                            + " reason=" + reason);
                } catch (Throwable error) {
                    Log.w(PryonProtocol.TAG, "Unable to disable native detector", error);
                }
                try {
                    Method destroy = findMethod(coreClass, "nDestroyNativeService", 0);
                    destroy.setAccessible(true);
                    Object result = destroy.invoke(core);
                    Log.i(PryonProtocol.TAG, "PRYON_NATIVE_DESTROYED result=" + result
                            + " reason=" + reason);
                } catch (Throwable error) {
                    Log.w(PryonProtocol.TAG, "Unable to destroy native detector", error);
                }
            }
            nativeCreated = false;
            core = null;
            coreClass = null;
            inputStream = null;
            metadataStream = null;
            callbackProxies = null;
            streamClass = null;
        }
    }

    private static Constructor<?> findCallbackConstructor(Class<?> type) throws Exception {
        for (Constructor<?> constructor : type.getDeclaredConstructors()) {
            Class<?>[] parameters = constructor.getParameterTypes();
            if (parameters.length != 3) continue;
            boolean interfaces = true;
            for (Class<?> parameter : parameters) interfaces &= parameter.isInterface();
            if (interfaces) return constructor;
        }
        throw new NoSuchMethodException("Expected three-callback constructor on " + type.getName());
    }

    private static Method findMethod(Class<?> type, String name, int parameterCount)
            throws Exception {
        for (Method method : type.getDeclaredMethods()) {
            if (name.equals(method.getName())
                    && method.getParameterTypes().length == parameterCount) return method;
        }
        throw new NoSuchMethodException(name + "/" + parameterCount + " on " + type.getName());
    }

    private static Object defaultValue(Class<?> type) {
        if (!type.isPrimitive() || type == void.class) return null;
        if (type == boolean.class) return false;
        if (type == byte.class) return (byte) 0;
        if (type == short.class) return (short) 0;
        if (type == int.class) return 0;
        if (type == long.class) return 0L;
        if (type == float.class) return 0f;
        if (type == double.class) return 0d;
        if (type == char.class) return (char) 0;
        return null;
    }

    @Override
    public void onDestroy() {
        cleanupNative("service_destroyed");
        if (eventClient != null) {
            eventClient.close();
            eventClient = null;
        }
        if (bindRequested) {
            try {
                unbindService(connection);
            } catch (Throwable error) {
                Log.w(PryonProtocol.TAG, "Unable to unbind audio provider", error);
            }
        }
        audioProvider = null;
        bindRequested = false;
        Log.i(PryonProtocol.TAG, "PRYON_SERVICE_DESTROYED");
        super.onDestroy();
        // libwakewordserver_jni.so is process-global. Exiting after orderly native
        // destruction prevents a later service start from reusing an incompatible
        // DexClassLoader/native namespace in this ART process.
        Process.killProcess(Process.myPid());
    }
}
