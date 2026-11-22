<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

use Illuminate\Mail\Mailable;
use Illuminate\Mail\Mailables\Content;
use Illuminate\Mail\Mailables\Envelope;
use Illuminate\Mail\Mailables\Headers;

/** Carries a finished {@see Email} through Laravel's mailer (so every configured mail transport works). */
final class KitMail extends Mailable
{
    public function __construct(public readonly Email $email) {}

    public function envelope(): Envelope
    {
        return new Envelope(subject: $this->email->subject, cc: $this->email->cc);
    }

    public function content(): Content
    {
        return new Content(
            html: $this->email->html !== '' ? 'laravelkit::raw-html' : null,
            text: $this->email->text !== '' ? 'laravelkit::raw-text' : null,
            with: ['htmlBody' => $this->email->html, 'textBody' => $this->email->text],
        );
    }

    public function headers(): Headers
    {
        return new Headers(text: ['X-Kit-Message-Id' => $this->email->id]);
    }
}
