use std::net::{IpAddr, Ipv4Addr};

use futures::TryStreamExt;
use rtnetlink::{AddressMessageBuilder, LinkDummy, LinkUnspec, LinkVlan, RouteMessageBuilder, new_connection};
use serde::{Deserialize, Serialize};

#[derive(Debug, Deserialize)]
pub struct Op {
    pub action: String,
    pub address: Option<String>,
    pub vlan: Option<u16>,
    pub destination: Option<String>,
    pub gateway: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct Report {
    pub link: String,
    pub addresses: Vec<String>,
    pub routes: Vec<String>,
    pub notes: Vec<String>,
}

struct Session {
    handle: rtnetlink::Handle,
}

impl Session {
    fn open() -> Result<Self, String> {
        let (conn, handle, _) = new_connection().map_err(|err| format!("netlink: {err}"))?;
        tokio::spawn(conn);
        Ok(Self { handle })
    }
}

pub async fn apply(link: &str, ops: &[Op]) -> Result<Report, String> {
    let session = Session::open()?;
    session.ensure_link(link).await?;
    let mut notes = Vec::new();
    let mut current = link.to_string();
    for op in ops {
        match op.action.as_str() {
            "add-vlan" => {
                let id = op.vlan.ok_or("add-vlan needs a vlan id")?;
                let name = vlan_name(id);
                session.ensure_vlan(link, &name, id).await?;
                current = name;
                notes.push(format!("vlan {id} on {link}"));
            }
            "add-address" => {
                let address = op.address.as_deref().ok_or("add-address needs an address")?;
                let note = session.add_address(&current, address).await?;
                if let Some(note) = note {
                    notes.push(note);
                }
            }
            "add-route" => {
                let destination = op.destination.as_deref().ok_or("add-route needs a destination")?;
                let gateway = op.gateway.as_deref().ok_or("add-route needs a gateway")?;
                let note = session.add_route(&current, destination, gateway).await?;
                if let Some(note) = note {
                    notes.push(note);
                }
            }
            other => return Err(format!("unknown action {other}")),
        }
    }
    let mut report = session.report(link).await?;
    report.notes = notes;
    Ok(report)
}

pub async fn remove(link: &str, ops: &[Op]) -> Result<Report, String> {
    let session = Session::open()?;
    let mut notes = Vec::new();
    let mut current = link.to_string();
    for op in ops {
        match op.action.as_str() {
            "add-vlan" => {
                let id = op.vlan.unwrap_or(0);
                current = vlan_name(id);
                if session.delete_link(&current).await? {
                    notes.push(format!("removed vlan device {current}"));
                } else {
                    notes.push(format!("vlan device {current} was already gone"));
                }
            }
            "add-address" => {
                let address = op.address.as_deref().ok_or("add-address needs an address")?;
                if session.delete_address(&current, address).await? {
                    notes.push(format!("removed {address}"));
                } else {
                    notes.push(format!("{address} was already gone"));
                }
            }
            "add-route" => {
                let destination = op.destination.as_deref().ok_or("add-route needs a destination")?;
                let gateway = op.gateway.as_deref().ok_or("add-route needs a gateway")?;
                if session.delete_route(&current, destination, gateway).await? {
                    notes.push(format!("removed route {destination}"));
                } else {
                    notes.push(format!("route {destination} was already gone"));
                }
            }
            other => return Err(format!("unknown action {other}")),
        }
    }
    let mut report = match session.report(link).await {
        Ok(report) => report,
        Err(_) => Report { link: link.to_string(), addresses: Vec::new(), routes: Vec::new(), notes: Vec::new() },
    };
    report.notes = notes;
    Ok(report)
}

pub async fn prepare(link: &str) -> Result<(), String> {
    Session::open()?.ensure_link(link).await
}

pub async fn report(link: &str) -> Result<Report, String> {
    Session::open()?.report(link).await
}

pub async fn self_test() -> Result<(), String> {
    let link = "span0";
    let session = Session::open()?;
    session.ensure_link(link).await?;
    let addr = "10.20.30.1/24";
    session.add_address(link, addr).await?;
    let again = session.add_address(link, addr).await?;
    if again.as_deref() != Some("10.20.30.1/24 was already present") {
        return Err(format!("second add should notice the address, got {again:?}"));
    }
    let listed = session.report(link).await?;
    if !listed.addresses.iter().any(|item| item.starts_with("10.20.30.1/")) {
        return Err(format!("address missing after add: {:?}", listed.addresses));
    }
    session.add_route(link, "192.0.2.0/24", "10.20.30.1").await?;
    let again_route = session.add_route(link, "192.0.2.0/24", "10.20.30.1").await?;
    if again_route.as_deref() != Some("192.0.2.0/24 was already present") {
        return Err(format!("second route add should notice the route, got {again_route:?}"));
    }
    let listed = session.report(link).await?;
    if !listed.routes.iter().any(|item| item.contains("192.0.2.0/24")) {
        return Err(format!("route missing after add: {:?}", listed.routes));
    }
    if !session.delete_route(link, "192.0.2.0/24", "10.20.30.1").await? {
        return Err("route delete reported already gone".into());
    }
    if session.delete_route(link, "192.0.2.0/24", "10.20.30.1").await? {
        return Err("second route delete should report already gone".into());
    }
    if !session.delete_address(link, addr).await? {
        return Err("address delete reported already gone".into());
    }
    if session.delete_address(link, addr).await? {
        return Err("second address delete should report already gone".into());
    }
    let listed = session.report(link).await?;
    if listed.addresses.iter().any(|item| item.starts_with("10.20.30.1/")) {
        return Err(format!("address still present: {:?}", listed.addresses));
    }
    session.ensure_vlan(link, "span100", 100).await?;
    session.add_address("span100", "10.20.40.1/24").await?;
    let listed = session.report("span100").await?;
    if !listed.addresses.iter().any(|item| item.starts_with("10.20.40.1/")) {
        return Err(format!("vlan address missing: {:?}", listed.addresses));
    }
    Ok(())
}

impl Session {
    async fn ensure_link(&self, name: &str) -> Result<(), String> {
        if self.link_index(name).await.is_ok() {
            return Ok(());
        }
        self.handle
            .link()
            .add(LinkDummy::new(name).build())
            .execute()
            .await
            .map_err(|err| format!("create {name}: {err}"))?;
        let index = self.link_index(name).await?;
        self.handle
            .link()
            .set(LinkUnspec::new_with_index(index).up().build())
            .execute()
            .await
            .map_err(|err| format!("up {name}: {err}"))?;
        Ok(())
    }

    async fn ensure_vlan(&self, parent: &str, name: &str, id: u16) -> Result<(), String> {
        if self.link_index(name).await.is_ok() {
            return Ok(());
        }
        let parent_index = self.link_index(parent).await?;
        self.handle
            .link()
            .add(LinkVlan::new(name, parent_index, id).up().build())
            .execute()
            .await
            .map_err(|err| format!("create vlan {name}: {err}"))?;
        Ok(())
    }

    async fn add_address(&self, link: &str, cidr: &str) -> Result<Option<String>, String> {
        let index = self.link_index(link).await?;
        let (ip, prefix) = parse_cidr(cidr)?;
        let ipv4 = ipv4(ip)?;
        match self.handle.address().add(index, IpAddr::V4(ipv4), prefix).execute().await {
            Ok(()) => Ok(None),
            Err(err) if exists(&err) => Ok(Some(format!("{cidr} was already present"))),
            Err(err) => Err(format!("add address {cidr}: {err}")),
        }
    }

    async fn delete_address(&self, link: &str, cidr: &str) -> Result<bool, String> {
        let Ok(index) = self.link_index(link).await else {
            return Ok(false);
        };
        let (ip, prefix) = parse_cidr(cidr)?;
        let ipv4 = ipv4(ip)?;
        let message = AddressMessageBuilder::<Ipv4Addr>::new().index(index).address(ipv4, prefix).build();
        match self.handle.address().del(message).execute().await {
            Ok(()) => Ok(true),
            Err(err) if missing(&err) => Ok(false),
            Err(err) => Err(format!("delete address {cidr}: {err}")),
        }
    }

    async fn add_route(&self, link: &str, destination: &str, gateway: &str) -> Result<Option<String>, String> {
        let index = self.link_index(link).await?;
        let (dest, prefix) = parse_cidr(destination)?;
        let via = ipv4(gateway.parse().map_err(|err| format!("gateway: {err}"))?)?;
        let message = RouteMessageBuilder::<Ipv4Addr>::new()
            .destination_prefix(ipv4(dest)?, prefix)
            .gateway(via)
            .output_interface(index)
            .build();
        match self.handle.route().add(message).execute().await {
            Ok(()) => Ok(None),
            Err(err) if exists(&err) => Ok(Some(format!("{destination} was already present"))),
            Err(err) => Err(format!("add route {destination}: {err}")),
        }
    }

    async fn delete_route(&self, link: &str, destination: &str, gateway: &str) -> Result<bool, String> {
        let Ok(index) = self.link_index(link).await else {
            return Ok(false);
        };
        let (dest, prefix) = parse_cidr(destination)?;
        let via = ipv4(gateway.parse().map_err(|err| format!("gateway: {err}"))?)?;
        let message = RouteMessageBuilder::<Ipv4Addr>::new()
            .destination_prefix(ipv4(dest)?, prefix)
            .gateway(via)
            .output_interface(index)
            .build();
        match self.handle.route().del(message).execute().await {
            Ok(()) => Ok(true),
            Err(err) if missing(&err) => Ok(false),
            Err(err) => Err(format!("delete route {destination}: {err}")),
        }
    }

    async fn delete_link(&self, name: &str) -> Result<bool, String> {
        let Ok(index) = self.link_index(name).await else {
            return Ok(false);
        };
        self.handle
            .link()
            .del(index)
            .execute()
            .await
            .map_err(|err| format!("delete link {name}: {err}"))?;
        Ok(true)
    }

    async fn report(&self, link: &str) -> Result<Report, String> {
        let index = self.link_index(link).await?;
        let mut addresses = Vec::new();
        let mut listed = self.handle.address().get().set_link_index_filter(index).execute();
        while let Some(message) = listed.try_next().await.map_err(|err| format!("list addresses: {err}"))? {
            if let Some(text) = address_text(&message) {
                addresses.push(text);
            }
        }
        let mut routes = Vec::new();
        let mut listed = self.handle.route().get(RouteMessageBuilder::<Ipv4Addr>::new().output_interface(index).build()).execute();
        while let Some(message) = listed.try_next().await.map_err(|err| format!("list routes: {err}"))? {
            if let Some(text) = route_text(&message) {
                routes.push(text);
            }
        }
        Ok(Report { link: link.to_string(), addresses, routes, notes: Vec::new() })
    }

    async fn link_index(&self, name: &str) -> Result<u32, String> {
        let mut links = self.handle.link().get().match_name(name.to_string()).execute();
        let Some(link) = links.try_next().await.map_err(|err| format!("get link {name}: {err}"))? else {
            return Err(format!("link {name} does not exist"));
        };
        Ok(link.header.index)
    }
}

fn vlan_name(id: u16) -> String {
    format!("span{id}")
}

fn parse_cidr(value: &str) -> Result<(IpAddr, u8), String> {
    let (addr, prefix) = value.split_once('/').ok_or_else(|| format!("not a cidr: {value}"))?;
    let ip: IpAddr = addr.parse().map_err(|err| format!("ip {addr}: {err}"))?;
    let len: u8 = prefix.parse().map_err(|err| format!("prefix {prefix}: {err}"))?;
    Ok((ip, len))
}

fn ipv4(addr: IpAddr) -> Result<Ipv4Addr, String> {
    match addr {
        IpAddr::V4(ip) => Ok(ip),
        IpAddr::V6(_) => Err("the lab helper applies IPv4".into()),
    }
}

fn exists(err: &rtnetlink::Error) -> bool {
    let text = err.to_string().to_lowercase();
    text.contains("exist") || text.contains("eexist")
}

fn missing(err: &rtnetlink::Error) -> bool {
    let text = err.to_string().to_lowercase();
    text.contains("no such") || text.contains("not found") || text.contains("enoent") || text.contains("esrch")
}

fn address_text(message: &netlink_packet_route::address::AddressMessage) -> Option<String> {
    use netlink_packet_route::address::AddressAttribute;
    let mut ip = None;
    for attr in &message.attributes {
        if let AddressAttribute::Address(addr) = attr {
            ip = Some(*addr);
        }
    }
    let ip = ip?;
    Some(format!("{ip}/{}", message.header.prefix_len))
}

fn route_text(message: &netlink_packet_route::route::RouteMessage) -> Option<String> {
    use netlink_packet_route::route::RouteAttribute;
    let mut dest = None;
    let mut via = None;
    for attr in &message.attributes {
        match attr {
            RouteAttribute::Destination(addr) => dest = Some(route_addr(addr)),
            RouteAttribute::Gateway(addr) => via = Some(route_addr(addr)),
            _ => {}
        }
    }
    let dest = dest?;
    let prefix = message.header.destination_prefix_length;
    let mut text = format!("{dest}/{prefix}");
    if let Some(via) = via {
        text.push_str(&format!(" via {via}"));
    }
    Some(text)
}

fn route_addr(addr: &netlink_packet_route::route::RouteAddress) -> String {
    use netlink_packet_route::route::RouteAddress;
    match addr {
        RouteAddress::Inet(ip) => ip.to_string(),
        RouteAddress::Inet6(ip) => ip.to_string(),
        other => format!("{other:?}"),
    }
}
