package io.orisun.client;

public class OrisunException extends Exception {
    public OrisunException(String message) {
        super(message);
    }

    public OrisunException(String message, Throwable cause) {
        super(message, cause);
    }
}
