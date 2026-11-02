# Protocol: np-community

Routes: `/basic_view/sec/get_self_profile`, `/basic_view/func/get_profile`,
`/basic_update/sec/update_ptl`, `/basic_update/sec/update_ci`, `/a/{cap}`,
`/np/resource/title/{title}/matching/{resource}`, `/healthz`.
No native login or trophy routes are registered here.

## NSX feeds

`GET`/`HEAD https://nsx.sec.<env>.dl.playstation.net/nsx/sec/Xz78TMQ1Uf31VCYr/c/NSXWSV/NSXWSV-PN.P3.[GAME.|VIDEO.]<CC>-<FEED>.xml`,
read by `explore_category_psn` (firmware 4.93). The XMB requests the Game and Video column
`BILLBOARD0000001`, `XMB_TV_INSTALL01` and `XMB_COLUMN_VIDEO` at every sign-in; What's New reads
`WHATSNEW00000001`. Served from the embedded `community/nsx` tree; the country is dropped from the
name (`GAME-BILLBOARD.xml`), so items must not depend on it.

Shape, from the parser (attribute vs child as the firmware reads them):

```xml
<nsx anno="max-age=3600">            <!-- root: optional min-sys-ver; anno max-age = cache seconds, default 86400 -->
 <spc>                               <!-- or sub-spc -->
  <mtrl id="..." from="" until="" lastm="" anno="new=1&picks=1&emergency=0&rtype=0">
   <name/> <owner/> <desc/>          <!-- child text, 40/40/80 bytes -->
   <lang>all</lang>                  <!-- optional, repeatable: CELL_SYSUTIL_LANG number or "all" -->
   <cntry agelmt="0" uagelmt="99">pt</cntry>  <!-- optional, repeatable: country or "all" -->
   <url>https://.../x.png</url>      <!-- png/jpg/jpeg/gif image, swf; up to 2048 bytes -->
   <data type="120">base64</data>    <!-- optional, 1024 bytes decoded -->
   <target type="p|c|u">...</target> <!-- product id, store category, or http(s) URL (mp4 on .dl.playstation.net plays) -->
  </mtrl>
 </spc>
</nsx>
```

The root element name and the `from`/`until`/`lastm` date format are not confirmed; a value
the firmware cannot parse leaves the item always valid.

## Scope

Profiles, public avatar capabilities, canonical core friends and leased XMPP presence. No local identity or relationship database; profile and XMPP authentication verify np-account tickets and current core standing.