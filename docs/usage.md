# Feature Usage

This section explains how to use each high-level feature and which commands are helpful for them.

## Web Dashboard
The easiest way to manage your bot configuration is through the web dashboard.
Helpful commands include:
* `/dashboard` to get a secure link to manage your server's settings, mappings, and configuration via the web UI.

## Automated Role Synchronization
To use automated role synchronization, you need to map Discord roles to EVE entities.
Helpful commands include:
* `/map_corp` to automatically map a Discord role to a corporation with the exact same name.
* `/map_alliance` to map an EVE Alliance ID to a Discord role.
* `/map_char_id` to manually link a specific character to a user.
* `/sync` to force an immediate manual verification of all mapped users.
* `/report` to generate a report of unmapped users and clean up old members.
* `/exclude_sync` to exclude specific users from being synchronized.

## EVE Mail Forwarding
To enable EVE Mail forwarding, you must configure a channel for the bot to post messages in.
Helpful commands include:
* `/set_mail_channel` to designate the channel where EVE mails will be posted.

## Wanderer Connection Tracking

The bot can watch your [wanderer](https://github.com/wanderer-industries/wanderer) maps and post a message whenever a new solar system connects to a tracked J-space system. First set your wanderer PostgreSQL connection string in the dashboard's Wanderer Integration card, then:
Helpful commands include:
* `/channel_wanderer` to designate the channel for notifications.
* `/wanderer_track` to start tracking a system (e.g. J111011).
* `/wanderer_untrack` to stop tracking a system.

## Standings Management
You can assign roles based on in-game standings to automatically organize your server members.
Helpful commands include:
* `/map_standing_terrible` to assign a role for terrible standing.
* `/map_standing_bad` to assign a role for bad standing.
* `/map_standing_neutral` to assign a role for neutral standing.
* `/map_standing_good` to assign a role for good standing.
* `/map_standing_excellent` to assign a role for excellent standing.
* `/exclude_corp_standing` to prevent a specific mapped corporation from receiving standing roles.

## Event Tracking
To keep your server updated with in-game events, set an events channel.
Helpful commands include:
* `/set_events_channel` to choose where newly created calendar events appear.

## Security and Authentication
Enforce registration and verification for your server members.
Helpful commands include:
* `/toggle_2fa` to require a verification code sent via EVE mail.
* `/seat_needed_roles` to require SeAT registration for specific corporation or alliance roles.
* `/set_missing_seat_role` to automatically tag users lacking SeAT registration and prompt them in the greeting channel.
* `/exclude_2fa` to bypass the verification requirement for certain roles.