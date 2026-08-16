package com.echolocal.pryon;

final class PryonProtocol {
    static final String TAG = "EchoLocalPryon";
    static final String DESCRIPTOR = "com.echolocal.pryon.AudioProvider";
    static final String PCM_SOCKET = "echolocal-pryon-pcm";
    static final int GET_STREAM = 0x455001;

    static final String EXTRA_AMAZON_APK = "amazon_apk";
    static final String EXTRA_ALEXA_MODEL = "alexa_model";
    static final String EXTRA_AED_MODEL = "aed_model";

    private PryonProtocol() { }
}
