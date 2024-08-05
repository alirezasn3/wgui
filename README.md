# WGUI - WireGuard User Interface

WGUI is a web-based user interface designed to simplify the management of WireGuard VPN configurations and peers. It provides an intuitive interface for creating, editing, and managing VPN peers, with a focus on ease of use and accessibility.

## Features

- **Peer Management**: Easily create, edit, and manage WireGuard peers.
- **Usage Monitoring**: Monitor data usage and reset statistics for peers.
- **Configuration Sharing**: Share or download WireGuard configurations for peers.
- **QR Code Generation**: Generate QR codes for easy mobile configuration.
- **Role-Based Access Control**: Admin, distributor, and user roles with specific permissions.
- **Prerendering Support**: Utilizes SvelteKit prerendering for improved performance.

## Peer Management and Roles

In WGUI, peers can be managed effectively through the interface, which allows for the creation, modification, and deletion of peer configurations. Peers can also be disabled, which is a feature that temporarily removes them from the active VPN configuration without permanently deleting their data. This allows for easy reactivation of peers when needed.

### Roles

WGUI introduces three distinct roles, each with its own set of permissions and capabilities:

- **Admin**: Has full access to all features and settings within WGUI. Admins can create, edit, delete, and disable peers. They can also manage roles for other users, including setting users as distributors or admins.
- **Distributor**: A role designed for users who manage a subset of peers. Distributors can create new peers and have access to configurations and management options for peers they've created. However, they cannot modify peers created by others or access global settings.
- **User**: The most restricted role, intended for end-users. Users can view and manage their own peer configurations but cannot create new peers or access any administrative features.

### Disabling Peers

Peers can be disabled by users with the necessary permissions (admins and possibly distributors, depending on the peer's ownership). Disabling a peer effectively removes it from the active VPN configuration without deleting its configuration data. This feature is useful for temporarily revoking access without the need to completely reconfigure a peer if access needs to be restored later.

### Roles

WGUI features three roles with distinct permissions:

- **Admin**: Full access to all features and settings.
- **Distributor**: Can manage peers they've created but cannot access global settings.
- **User**: Can view and manage their own peer configurations.

## Configuration

A sample `config.json` file based on the `Config` struct in `main.go` looks like this:

```json
{
  "mongoURI": "mongodb://localhost:27017",
  "dbName": "wgui",
  "collectionName": "peers",
  "interfaceName": "wg0",
  "interfaceAddress": "10.0.0.1",
  "interfaceAddressCIDR": "10.0.0.1/24",
  "publicAddress": "wg.example.com",
  "endpoints": ["wg.example.com:51820"]
}
```

Replace the placeholder values with your actual configuration details.
