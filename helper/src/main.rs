use std::process::ExitCode;

use serde::Deserialize;
use tiny_http::{Header, Method, Response, Server, StatusCode};

mod nl;

const DEFAULT_LINK: &str = "span0";

#[derive(Debug, Deserialize)]
struct Request {
    link: Option<String>,
    ops: Vec<nl::Op>,
}

fn main() -> ExitCode {
    let mut args = std::env::args().skip(1);
    match args.next().as_deref() {
        Some("self-test") => match self_test() {
            Ok(()) => ExitCode::SUCCESS,
            Err(err) => {
                eprintln!("self-test failed: {err}");
                ExitCode::from(1)
            }
        },
        Some("serve") => {
            let mut listen = "0.0.0.0:8080".to_string();
            let mut link = DEFAULT_LINK.to_string();
            while let Some(arg) = args.next() {
                match arg.as_str() {
                    "--listen" => listen = args.next().unwrap_or(listen),
                    "--link" => link = args.next().unwrap_or(link),
                    other => {
                        eprintln!("unknown argument {other}");
                        return ExitCode::from(2);
                    }
                }
            }
            if let Err(err) = serve(&listen, &link) {
                eprintln!("serve failed: {err}");
                return ExitCode::from(1);
            }
            ExitCode::SUCCESS
        }
        _ => {
            eprintln!("usage: span-helper serve --listen 0.0.0.0:8080 --link span0 | span-helper self-test");
            ExitCode::from(2)
        }
    }
}

fn serve(listen: &str, default_link: &str) -> Result<(), String> {
    let server = Server::http(listen).map_err(|err| err.to_string())?;
    let runtime = tokio::runtime::Runtime::new().map_err(|err| err.to_string())?;
    runtime.block_on(nl::prepare(default_link))?;
    for mut req in server.incoming_requests() {
        let path = req.url().to_string();
        let method = req.method().clone();
        let mut body = String::new();
        if let Err(err) = std::io::Read::read_to_string(req.as_reader(), &mut body) {
            let _ = req.respond(text(StatusCode(400), err.to_string()));
            continue;
        }
        let result = runtime.block_on(async {
            handle(method, &path, &body, default_link).await
        });
        let response = match result {
            Ok(report) => {
                let bytes = serde_json::to_vec(&report).unwrap_or_else(|_| b"{}".to_vec());
                Response::from_data(bytes).with_status_code(StatusCode(200))
            }
            Err(err) => text(StatusCode(500), err),
        };
        let _ = req.respond(response);
    }
    Ok(())
}

async fn handle(method: Method, path: &str, body: &str, default_link: &str) -> Result<nl::Report, String> {
    let request: Request = if body.is_empty() {
        Request { link: None, ops: Vec::new() }
    } else {
        serde_json::from_str(body).map_err(|err| format!("json: {err}"))?
    };
    let link = request.link.unwrap_or_else(|| default_link.to_string());
    match (&method, path) {
        (Method::Post, "/apply") => nl::apply(&link, &request.ops).await,
        (Method::Post, "/remove") => nl::remove(&link, &request.ops).await,
        (Method::Get, "/list") => nl::report(&link).await,
        _ => Err(format!("unknown {method} {path}")),
    }
}

fn text(code: StatusCode, body: String) -> Response<std::io::Cursor<Vec<u8>>> {
    let header = Header::from_bytes(&b"Content-Type"[..], &b"text/plain"[..]).expect("header");
    Response::from_string(body).with_status_code(code).with_header(header)
}

fn self_test() -> Result<(), String> {
    nix::sched::unshare(nix::sched::CloneFlags::CLONE_NEWNET).map_err(|err| format!("unshare: {err}"))?;
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|err| err.to_string())?;
    runtime.block_on(nl::self_test())
}
