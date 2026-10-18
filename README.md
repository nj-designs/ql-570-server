# ql-570-server

A small web server for printing images on a Brother QL-570 label printer. Upload a
PNG, JPEG or GIF, see a black-and-white preview at the printer's real resolution,
and print it.

- Reads the loaded label roll from the printer and refuses to print on the wrong one.
- Supports every QL-570 media size: continuous 12/29/38/50/54/62 mm, die-cut
  17×54, 17×87, 23×23, 29×42, 29×90, 38×90, 39×48, 52×29, 62×29, 62×100 mm and
  round Ø12/24/58 mm.
- Rotates images so their long side matches the label, then scales to fit. On
  continuous rolls the label length follows the image.
- Threshold (text, logos, barcodes) or Floyd–Steinberg dither (photos)
  conversion, with an adjustable level.
- Copies, cut after each label, waits for the printer to confirm each label.
- Single static binary, standard library only. Talks to the kernel `usblp`
  device directly; no CUPS or Brother driver needed.

## Build and run

```sh
go build -o ql570-server .
./ql570-server -addr :8080 -device /dev/usb/lp0
```

Then open `http://<host>:8080/`.

| Flag          | Default        | Meaning                     |
|---------------|----------------|-----------------------------|
| `-addr`       | `:8080`        | HTTP listen address         |
| `-device`     | `/dev/usb/lp0` | Printer device file         |
| `-max-upload` | 32 MiB         | Largest accepted upload     |

The device node is owned by group `lp`, so the user running the server must be
in that group:

```sh
sudo usermod -aG lp $USER   # then log out and back in
```

## Install as a service

```sh
sudo install ql570-server /usr/local/bin/
sudo cp deploy/ql570-server.service /etc/systemd/system/
sudo systemctl enable --now ql570-server
```

The service has no authentication and listens on every interface. Run it only on
a trusted network.

`deploy/99-ql570.rules` is an optional udev rule that adds a stable
`/dev/usb/ql570` symlink, which helps if more than one USB printer is attached.

## API

| Method & path       | Purpose                                                    |
|---------------------|------------------------------------------------------------|
| `GET /api/media`    | Supported media and their printable sizes in dots          |
| `GET /api/status`   | Printer state and detected media                           |
| `POST /api/preview` | Multipart form → PNG preview of the label                  |
| `POST /api/print`   | Multipart form → print; JSON `{message}` or `{error}`      |

Form fields: `image` (file), `media` (ID from `/api/media`, such as `62` or
`29x90`), `rotate` (`auto`, `0`, `90`, `180`, `270`), `mode` (`threshold` or
`dither`), `threshold` (1–254, default 128), `copies` (1–100) and `autocut`
(`true`/`false`).

```sh
curl -F image=@logo.png -F media=62 -F copies=2 http://localhost:8080/api/print
```

## Protocol notes

The printer is driven with Brother's raster command set, the same protocol used by
[sudomesh/ql570](https://github.com/sudomesh/ql570). Media geometry follows
Brother's QL raster command reference. Each raster line is 90 bytes (720 head
pins at 300 dpi). The printable area sits at a media-specific offset, and the pin
order is mirrored relative to the image.

## License

MIT. See [LICENSE](LICENSE).
