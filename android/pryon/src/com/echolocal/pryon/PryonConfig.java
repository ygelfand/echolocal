package com.echolocal.pryon;

import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;

import java.io.File;

final class PryonConfig {
    private static final String PREFS = "pryon";

    final String amazonApk;
    final String alexaModel;
    final String aedModel;

    private PryonConfig(String amazonApk, String alexaModel, String aedModel) {
        this.amazonApk = amazonApk;
        this.alexaModel = alexaModel;
        this.aedModel = aedModel;
    }

    static PryonConfig load(Context context) {
        SharedPreferences prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        return new PryonConfig(
                prefs.getString(PryonProtocol.EXTRA_AMAZON_APK, null),
                prefs.getString(PryonProtocol.EXTRA_ALEXA_MODEL, null),
                prefs.getString(PryonProtocol.EXTRA_AED_MODEL, null));
    }

    static boolean updateFromIntent(Context context, Intent intent) {
        if (intent == null) return false;
        boolean any = intent.hasExtra(PryonProtocol.EXTRA_AMAZON_APK)
                || intent.hasExtra(PryonProtocol.EXTRA_ALEXA_MODEL)
                || intent.hasExtra(PryonProtocol.EXTRA_AED_MODEL);
        if (!any) return false;

        String amazonApk = intent.getStringExtra(PryonProtocol.EXTRA_AMAZON_APK);
        String alexaModel = intent.getStringExtra(PryonProtocol.EXTRA_ALEXA_MODEL);
        String aedModel = intent.getStringExtra(PryonProtocol.EXTRA_AED_MODEL);
        PryonConfig supplied = new PryonConfig(amazonApk, alexaModel, aedModel);
        supplied.validate();

        PryonConfig current = load(context);
        if (same(current.amazonApk, supplied.amazonApk)
                && same(current.alexaModel, supplied.alexaModel)
                && same(current.aedModel, supplied.aedModel)) {
            return false;
        }

        SharedPreferences prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        if (!prefs.edit()
                .putString(PryonProtocol.EXTRA_AMAZON_APK, amazonApk)
                .putString(PryonProtocol.EXTRA_ALEXA_MODEL, alexaModel)
                .putString(PryonProtocol.EXTRA_AED_MODEL, aedModel)
                .commit()) {
            throw new IllegalStateException("Unable to persist Pryon configuration");
        }
        return true;
    }

    private static boolean same(String left, String right) {
        return left == null ? right == null : left.equals(right);
    }

    void validate() {
        requireReadableFile("SpeechInteractionManager APK", amazonApk);
        requireReadableFile("Alexa manifest", alexaModel);
        requireReadableFile("AED manifest", aedModel);
    }

    String describe() {
        return "amazon_apk=" + amazonApk + ", alexa_model=" + alexaModel
                + ", aed_model=" + aedModel;
    }

    private static void requireReadableFile(String label, String path) {
        if (path == null || path.length() == 0) {
            throw new IllegalStateException(label + " path was not configured");
        }
        File file = new File(path);
        if (!file.isAbsolute() || !file.isFile() || !file.canRead()) {
            throw new IllegalStateException(label + " is not a readable absolute file: " + path);
        }
    }
}
