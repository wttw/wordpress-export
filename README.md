# wordpress-export
Suck posts from WordPress via the API and save them as markdown files

## Features

 * Works entirely via the WordPress API, no need to install anything in WordPress
 * Fetches only public content, so no authentication needed
 * Fetches posts, authors, tags and categories and saves them as one markdown file per post
 * Includes tags, categories and authors in the markdown frontmatter
 * Exports content usable by any markdown file based CMS or site generator, such as Gatsby or Netlify CMS
 * No size limits, it handles thousands of posts
 * Fetches images and documents each post links to and saves them alongside the inedx.md file, rewriting links to point to that local copy
 * Support fetching only a sample of posts, for faster builds during development
 * It can add static yaml to the frontmatter of each post, so you can extend the schema easily
  
## Usage

To fetch all posts and assets from your blog, and save them as a tree in a directory "output":

`wordpress-export https://your-blog-host.com`

To fetch the 20 newest posts, saving them as MDX files with featured post and draft settings added:

`wordpress-export --sample=20 --postfile=index.mdx --frontmatter=frontmatter.yml`

where frontmatter.yml contains

```yaml
featuredPost: false
draft: false
```

Other flags:
```
Usage: wordpress-export [flags] <your blog url>
      --api string           Base URL of the WordPress API
      --assets string        Copy assets under this path (default "/wp-content/uploads/")
      --frontmatter string   Read additional frontmatter from this file
  -h, --help                 Show this help
      --log string           Log progress to this file
      --meta                 save tags, categories and authors
  -o, --output string        Save results to this directory (default "./output")
      --postfile string      The filename for each post (default "index.md")
      --prefix string        
  -q, --quiet                Don't print progress
      --sample int           Only retrieve this many posts
      --silent               Don't print progress or warnings
  -V, --version              Show version

```

## Installation

Download the file from the [github release page](https://github.com/wttw/wordpress-export/releases/latest), for your operating system, unzip it and put it somewhere on your path. (If you're on Windows you can open a command prompt, cd to the directory where you unzipped it and run it from there.)

## Compilation

```shell script
git clone github.com/wttw/wordpress-export
cd wordpress-export
go build
```

## Missing Features

It only exports published posts, not drafts. It doesn't export pages, comments or anything other than posts, tags, categories and authors.

I'll probably add support for comments once I work out whether I'm using [StaticMan](https://staticman.net/) or [Schnack](https://schnack.cool/) or something else on my blog.

## Support

Put any issues or requests as a [github issue](https://github.com/wttw/wordprss-export/issues). I'll read issues but I'm not committing to fix everything. Pull requests welcome.

If you find it useful, buy me a coffee. [![ko-fi](https://www.ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/H2H31UQKT)